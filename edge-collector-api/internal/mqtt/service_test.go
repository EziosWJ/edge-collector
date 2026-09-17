package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/audit"
)

type mqttServiceRuntimeFake struct {
	state        RuntimeSnapshot
	refreshCalls int
	testCalls    int
	testConfig   RuntimeConfig
	refreshErr   error
	testErr      error
}

func (f *mqttServiceRuntimeFake) Refresh(context.Context) error {
	f.refreshCalls++
	return f.refreshErr
}

func (f *mqttServiceRuntimeFake) State() RuntimeSnapshot { return f.state }

func (f *mqttServiceRuntimeFake) TestConnection(_ context.Context, config RuntimeConfig) error {
	f.testCalls++
	f.testConfig = config
	return f.testErr
}

type mqttServiceConfigurerFake struct {
	builder   TopicBuilder
	interval  time.Duration
	retention time.Duration
	pending   int
	calls     int
}

func (f *mqttServiceConfigurerFake) Configure(builder TopicBuilder, value time.Duration) {
	f.builder = builder
	f.interval = value
	f.retention = value
	f.calls++
}

func (f *mqttServiceConfigurerFake) PendingCount() int { return f.pending }

type mqttServiceIntakeConfigurerFake struct {
	builder TopicBuilder
	calls   int
}

func (f *mqttServiceIntakeConfigurerFake) Configure(builder TopicBuilder) {
	f.builder = builder
	f.calls++
}

type mqttServiceQueueConfigurerFake struct {
	capacity int
	fairness int
	calls    int
}

func (f *mqttServiceQueueConfigurerFake) SetCommandQueueConfig(capacity, fairness int) {
	f.capacity = capacity
	f.fairness = fairness
	f.calls++
}

func TestServiceUpdateConfigRefreshesOnlyMQTTIntegrations(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &mqttServiceRuntimeFake{}
	projector := &mqttServiceConfigurerFake{pending: 3}
	events := &mqttServiceConfigurerFake{}
	intake := &mqttServiceIntakeConfigurerFake{}
	queue := &mqttServiceQueueConfigurerFake{}
	service, err := NewService(repository, runtime, projector, events, intake, queue)
	if err != nil {
		t.Fatal(err)
	}

	input := configInput(current)
	input.EdgeID = "edge-managed"
	input.TopicPrefix = "managed"
	input.RawPublishIntervalMS = 250
	input.OutboxRetentionDays = 11
	view, err := service.UpdateConfig(context.Background(), audit.Metadata{ActorID: 7, RequestID: "request-46"}, input)
	if err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}
	if view.EdgeID != "edge-managed" || view.TopicPrefix != "managed" {
		t.Fatalf("config view = %#v", view)
	}
	if runtime.refreshCalls != 1 {
		t.Fatalf("MQTT refresh calls = %d, want 1", runtime.refreshCalls)
	}
	if projector.calls != 1 || projector.interval != 250*time.Millisecond || projector.builder.EdgeID() != "edge-managed" {
		t.Fatalf("latest projector configuration = %#v", projector)
	}
	if events.calls != 1 || events.retention != 11*24*time.Hour || events.builder.EdgeID() != "edge-managed" {
		t.Fatalf("event projector configuration = %#v", events)
	}
	if intake.calls != 1 || intake.builder.EdgeID() != "edge-managed" {
		t.Fatalf("command intake configuration = %#v", intake)
	}
	if queue.calls != 1 || queue.capacity != input.CommandQueueCapacity || queue.fairness != input.CommandPollFairness {
		t.Fatalf("command queue configuration = %#v", queue)
	}
}

func TestServiceTestConnectionUsesPreviewWithoutPersistingOrChangingRuntime(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime := &mqttServiceRuntimeFake{state: RuntimeSnapshot{State: RuntimeStateDisabled, Connected: false}}
	service, err := NewService(repository, runtime, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 17, 6, 0, 0, 0, time.UTC)
	service.SetClock(func() time.Time { return fixed })

	input := configInput(current)
	input.BrokerURL = "mqtt://test-broker:1883"
	input.Enabled = false
	input.PasswordAction = SecretSet
	input.Password = "password-only-in-memory"
	before, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.TestConnection(context.Background(), &input)
	if err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if !result.Success || result.RuntimeConnected {
		t.Fatalf("test result = %#v", result)
	}
	if runtime.testCalls != 1 || runtime.testConfig.BrokerURL != input.BrokerURL || runtime.testConfig.Enabled != 1 {
		t.Fatalf("test runtime config = %#v", runtime.testConfig)
	}
	if runtime.testConfig.Password != input.Password {
		t.Fatalf("preview password was not passed only to test connector")
	}
	after, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.BrokerURL != before.BrokerURL || after.PasswordCiphertext != before.PasswordCiphertext {
		t.Fatalf("test connection changed persisted config: before=%#v after=%#v", before, after)
	}
	if runtime.state.State != RuntimeStateDisabled || runtime.state.Connected {
		t.Fatalf("test connection changed formal runtime state = %#v", runtime.state)
	}
}

func TestServiceStateAndOutboxStatsExposeBoundedHealth(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	current, err := repository.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	input := configInput(current)
	input.OutboxMaxRows = 10
	input.OutboxMaxBytes = 1024
	if _, err := repository.SaveConfig(context.Background(), input, audit.Event{Action: "mqtt.test", Resource: "MQTT 配置"}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 9, 17, 5, 59, 45, 0, time.UTC)
	message := OutboxMessage{MessageID: "stats-message", MessageType: OutboxMessageTypeEvent, Topic: "edge/event", QoS: 1, Payload: "12345", PayloadBytes: 5, Priority: OutboxPriorityReliableEvent, CreatedAt: created}
	if err := repository.Enqueue(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	runtime := &mqttServiceRuntimeFake{state: RuntimeSnapshot{State: RuntimeStateReconnecting, Connected: false, LastError: "temporary"}}
	projector := &mqttServiceConfigurerFake{pending: 2}
	service, err := NewService(repository, runtime, projector, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.SetClock(func() time.Time { return time.Date(2026, 9, 17, 6, 0, 1, 0, time.UTC) })
	state, err := service.State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state.State != RuntimeStateReconnecting || state.PendingLatestCount != 2 || state.Outbox.Rows != 1 || state.Outbox.Bytes != 5 {
		t.Fatalf("state = %#v", state)
	}
	if state.Outbox.OldestAge != 16 || state.Outbox.MaxRows != 10 || state.Outbox.MaxBytes != 1024 {
		t.Fatalf("outbox health = %#v", state.Outbox)
	}
	if state.Outbox.RowUtilization != 0.1 || state.Outbox.ByteUtilization != 5.0/1024.0 {
		t.Fatalf("outbox utilization = %#v", state.Outbox)
	}
	stats, err := service.OutboxStats(context.Background())
	if err != nil || stats.Rows != 1 {
		t.Fatalf("OutboxStats() = %#v, error=%v", stats, err)
	}
}

func TestServiceTestConnectionFailureDoesNotExposeConnectorError(t *testing.T) {
	repository, _, cleanup := newSQLiteRepository(t)
	defer cleanup()
	runtime := &mqttServiceRuntimeFake{testErr: errors.New("password=must-not-be-returned")}
	service, err := NewService(repository, runtime, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.TestConnection(context.Background(), nil)
	if !errors.Is(err, ErrTestConnectionFailed) || stringsContains(err.Error(), "password=") {
		t.Fatalf("test connection error = %v", err)
	}
}

func stringsContains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
