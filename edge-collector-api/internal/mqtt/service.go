package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
	"gorm.io/gorm"
)

// RuntimeController is the management-facing part of the MQTT runtime. It
// deliberately does not expose its transport or any acquisition session.
type RuntimeController interface {
	Refresh(context.Context) error
	State() RuntimeSnapshot
	TestConnection(context.Context, RuntimeConfig) error
}

type latestStateConfigurer interface {
	Configure(TopicBuilder, time.Duration)
	PendingCount() int
}

type eventProjectorConfigurer interface {
	Configure(TopicBuilder, time.Duration)
}

type commandIntakeConfigurer interface {
	Configure(TopicBuilder)
}

// CommandQueueConfigurer updates only the bounded acquisition command queue
// policy. It does not reload channels or own a Modbus session.
type CommandQueueConfigurer interface {
	SetCommandQueueConfig(int, int)
}

// HandlerService is the public HTTP seam for the MQTT management API.
// Keeping the interface here lets the router tests exercise the API without
// reaching into persistence or the MQTT client.
type HandlerService interface {
	GetConfig(context.Context) (ConfigView, error)
	UpdateConfig(context.Context, audit.Metadata, ConfigInput) (ConfigView, error)
	TestConnection(context.Context, *ConfigInput) (TestConnectionView, error)
	State(context.Context) (RuntimeStateView, error)
	OutboxStats(context.Context) (OutboxStatsView, error)
	PageCommands(context.Context, CommandJournalQuery) (Page[CommandJournalView], error)
	FindCommand(context.Context, string) (*CommandJournalView, error)
}

// Service coordinates durable MQTT configuration with the already-created
// runtime projections. A successful configuration write only refreshes MQTT;
// it never restarts or rebuilds acquisition channels.
type Service struct {
	repository *Repository
	runtime    RuntimeController
	projector  latestStateConfigurer
	events     eventProjectorConfigurer
	intake     commandIntakeConfigurer
	queue      CommandQueueConfigurer
	now        func() time.Time
	logger     *slog.Logger
}

func NewService(repository *Repository, runtime RuntimeController, projector latestStateConfigurer, events eventProjectorConfigurer, intake commandIntakeConfigurer, queueConfigurers ...CommandQueueConfigurer) (*Service, error) {
	if repository == nil {
		return nil, errors.New("MQTT repository is required")
	}
	var queue CommandQueueConfigurer
	if len(queueConfigurers) > 0 {
		queue = queueConfigurers[0]
	}
	return &Service{
		repository: repository,
		runtime:    runtime,
		projector:  projector,
		events:     events,
		intake:     intake,
		queue:      queue,
		now:        func() time.Time { return time.Now().UTC() },
		logger:     slog.Default(),
	}, nil
}

func (s *Service) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	s.now = now
}

func (s *Service) SetLogger(logger *slog.Logger) {
	if logger != nil {
		s.logger = logger
	}
}

func (s *Service) GetConfig(ctx context.Context) (ConfigView, error) {
	config, err := s.repository.GetConfig(normalizeContext(ctx))
	if err != nil {
		return ConfigView{}, err
	}
	return config.View(), nil
}

func (s *Service) UpdateConfig(ctx context.Context, metadata audit.Metadata, input ConfigInput) (ConfigView, error) {
	ctx = normalizeContext(ctx)
	config, err := s.repository.SaveConfig(ctx, input, audit.Event{
		Action:   "mqtt.config.update",
		Resource: "MQTT 配置",
		Metadata: metadata,
	})
	if err != nil {
		return ConfigView{}, err
	}

	// The database commit is the source of truth. Reconfigure only the MQTT
	// projections/intake and signal the MQTT runtime; acquisition is untouched.
	s.applyConfig(config)
	if s.runtime != nil {
		if refreshErr := s.runtime.Refresh(ctx); refreshErr != nil && s.logger != nil {
			s.logger.ErrorContext(ctx, "refresh MQTT runtime after config update", "error_type", fmt.Sprintf("%T", refreshErr))
		}
	}
	return config.View(), nil
}

func (s *Service) TestConnection(ctx context.Context, input *ConfigInput) (TestConnectionView, error) {
	ctx = normalizeContext(ctx)
	if s.runtime == nil {
		return TestConnectionView{}, ErrRuntimeUnavailable
	}

	var config RuntimeConfig
	var err error
	if input == nil {
		config, err = s.repository.RuntimeConfig(ctx)
	} else {
		config, err = s.repository.PreviewRuntimeConfig(ctx, *input)
	}
	if err != nil {
		return TestConnectionView{}, err
	}
	if config.Enabled == 0 {
		config.Enabled = 1
	}

	// TestConnection is an isolated connector attempt. Its timeout is derived
	// from the submitted/current config and never mutates the live runtime.
	testContext, cancel := context.WithTimeout(ctx, time.Duration(config.ConnectTimeoutMS)*time.Millisecond)
	defer cancel()
	if err := s.runtime.TestConnection(testContext, config); err != nil {
		// Do not carry connector/library error text into the HTTP error path;
		// some clients include authentication material in diagnostics.
		return TestConnectionView{}, ErrTestConnectionFailed
	}
	return TestConnectionView{Success: true, RuntimeConnected: s.runtime.State().Connected}, nil
}

func (s *Service) State(ctx context.Context) (RuntimeStateView, error) {
	ctx = normalizeContext(ctx)
	config, err := s.repository.GetConfig(ctx)
	if err != nil {
		return RuntimeStateView{}, err
	}
	stats, err := s.outboxStats(ctx, config)
	if err != nil {
		return RuntimeStateView{}, err
	}
	state := RuntimeSnapshot{State: RuntimeStateDisabled}
	if s.runtime != nil {
		state = s.runtime.State()
	}
	pending := 0
	if s.projector != nil {
		pending = s.projector.PendingCount()
	}
	return RuntimeStateView{RuntimeSnapshot: state, PendingLatestCount: pending, Outbox: stats}, nil
}

func (s *Service) OutboxStats(ctx context.Context) (OutboxStatsView, error) {
	ctx = normalizeContext(ctx)
	config, err := s.repository.GetConfig(ctx)
	if err != nil {
		return OutboxStatsView{}, err
	}
	return s.outboxStats(ctx, config)
}

func (s *Service) PageCommands(ctx context.Context, query CommandJournalQuery) (Page[CommandJournalView], error) {
	return s.repository.PageCommands(normalizeContext(ctx), query)
}

func (s *Service) FindCommand(ctx context.Context, commandID string) (*CommandJournalView, error) {
	if commandID == "" {
		return nil, ErrCommandNotFound
	}
	value, err := s.repository.FindCommand(normalizeContext(ctx), commandID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCommandNotFound
	}
	return value, err
}

func (s *Service) applyConfig(config Config) {
	builder, err := NewTopicBuilder(config.TopicPrefix, config.EdgeID)
	if err != nil {
		// SaveConfig validates the same values before commit. Keep this guard so
		// a malformed legacy row cannot make a management write panic.
		if s.logger != nil {
			s.logger.Error("apply MQTT config failed", "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	interval := time.Duration(config.RawPublishIntervalMS) * time.Millisecond
	retention := time.Duration(config.OutboxRetentionDays) * 24 * time.Hour
	if s.projector != nil {
		s.projector.Configure(builder, interval)
	}
	if s.events != nil {
		s.events.Configure(builder, retention)
	}
	if s.intake != nil {
		s.intake.Configure(builder)
	}
	if s.queue != nil {
		s.queue.SetCommandQueueConfig(config.CommandQueueCapacity, config.CommandPollFairness)
	}
}

func (s *Service) outboxStats(ctx context.Context, config Config) (OutboxStatsView, error) {
	stats, err := s.repository.OutboxStats(ctx)
	if err != nil {
		return OutboxStatsView{}, err
	}
	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	var oldestAge int64
	if stats.OldestAt != nil && now.After(*stats.OldestAt) {
		oldestAge = int64(now.Sub(*stats.OldestAt) / time.Second)
	}
	return OutboxStatsView{
		Rows:            stats.Rows,
		Bytes:           stats.Bytes,
		OldestAge:       oldestAge,
		LastError:       cloneString(stats.LastError),
		MaxRows:         config.OutboxMaxRows,
		MaxBytes:        config.OutboxMaxBytes,
		RowUtilization:  utilization(stats.Rows, int64(config.OutboxMaxRows)),
		ByteUtilization: utilization(stats.Bytes, config.OutboxMaxBytes),
	}, nil
}

func utilization(used, maximum int64) float64 {
	if maximum <= 0 {
		return 0
	}
	return float64(used) / float64(maximum)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

var (
	ErrRuntimeUnavailable   = errors.New("MQTT_RUNTIME_UNAVAILABLE")
	ErrTestConnectionFailed = errors.New("MQTT_TEST_CONNECTION_FAILED")
)

var _ HandlerService = (*Service)(nil)
