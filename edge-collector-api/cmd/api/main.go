// Command api starts the Go REST API.
package main

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/EziosWJ/edge-collector/edge-collector-api/docs"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	acquisitionscript "github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition/script"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/app"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/auth"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/config"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/dept"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/dictionary"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/filemgmt"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/logmgmt"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/mqtt"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/notification"
	platformdatabase "github.com/EziosWJ/edge-collector/edge-collector-api/internal/platform/database"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/rbac"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/sysconfig"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/usermgmt"
)

const defaultUserPassword = "admin123"

// @title Edge Collector API
// @version 0.1.0
// @description Go backend migration platform API.
// @BasePath /
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}

	database, err := platformdatabase.Open(context.Background(), cfg.Database)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := database.Close(); err != nil {
			applicationLogger := slog.Default()
			applicationLogger.Error("close database", "error", err)
		}
	}()

	authService, err := newAuthService(database, cfg.JWT, cfg.Auth.LoginGuard)
	if err != nil {
		slog.Error("build authentication service", "error", err)
		os.Exit(1)
	}

	rbacService, err := rbac.NewService(rbac.NewRepository(database.GORM))
	if err != nil {
		slog.Error("build RBAC service", "error", err)
		os.Exit(1)
	}
	deptService, err := dept.NewService(dept.NewRepository(database.GORM))
	if err != nil {
		slog.Error("build department service", "error", err)
		os.Exit(1)
	}
	notificationRepository := notification.NewRepository(database.GORM)
	userService, err := usermgmt.NewService(usermgmt.NewRepository(database.GORM, notificationRepository), defaultUserPassword)
	if err != nil {
		slog.Error("build user service", "error", err)
		os.Exit(1)
	}
	dictionaryService, err := dictionary.NewService(dictionary.NewRepository(database.GORM))
	if err != nil {
		slog.Error("build dictionary service", "error", err)
		os.Exit(1)
	}
	configService := sysconfig.NewService(sysconfig.NewRepository(database.GORM))
	fileStorage, err := filemgmt.NewLocalStorage(cfg.File.StorageRoot)
	if err != nil {
		slog.Error("build file storage", "error", err)
		os.Exit(1)
	}
	fileService, err := filemgmt.NewService(filemgmt.NewRepository(database.GORM), fileStorage)
	if err != nil {
		slog.Error("build file service", "error", err)
		os.Exit(1)
	}
	logService, err := logmgmt.NewService(logmgmt.NewRepository(database.GORM), configService)
	if err != nil {
		slog.Error("build log service", "error", err)
		os.Exit(1)
	}
	notificationService, err := notification.NewService(notificationRepository)
	if err != nil {
		slog.Error("build notification service", "error", err)
		os.Exit(1)
	}

	acquisitionService, err := acquisition.NewService(acquisition.NewRepository(database.GORM))
	if err != nil {
		slog.Error("build acquisition service", "error", err)
		os.Exit(1)
	}
	scriptRuntime := acquisitionscript.NewRuntime(acquisitionscript.Options{
		Limits: acquisitionscript.Limits{
			MaxSourceBytes:         cfg.Acquisition.Script.MaxSourceBytes,
			MaxExecutionMs:         cfg.Acquisition.Script.MaxExecutionMs,
			MaxExecutionSteps:      cfg.Acquisition.Script.MaxExecutionSteps,
			MaxModbusOperations:    cfg.Acquisition.Script.MaxModbusOperations,
			MaxDelayMs:             cfg.Acquisition.Script.MaxDelayMs,
			MaxTotalDelayMs:        cfg.Acquisition.Script.MaxTotalDelayMs,
			MaxStateBytesPerDevice: cfg.Acquisition.Script.MaxStateBytesPerDevice,
			MaxEventsPerExecution:  cfg.Acquisition.Script.MaxEventsPerExecution,
			MaxEventsPerDevice:     cfg.Acquisition.Script.MaxEventsPerDevice,
			MaxEventPayloadBytes:   cfg.Acquisition.Script.MaxEventPayloadBytes,
			MaxPrintLines:          cfg.Acquisition.Script.MaxPrintLines,
			MaxPrintLineBytes:      cfg.Acquisition.Script.MaxPrintLineBytes,
		},
		PrintSink: acquisitionscript.PrintSinkFunc(func(ctx context.Context, entry acquisitionscript.PrintEntry) {
			slog.Default().InfoContext(ctx, "acquisition script print",
				"device_id", entry.DeviceID,
				"script_id", entry.ScriptID,
				"script_version_id", entry.ScriptVersionID,
				"version_no", entry.VersionNo,
				"channel_id", entry.ChannelID,
				"message", entry.Message,
			)
		}),
	})
	acquisitionService.SetScriptValidator(acquisition.NewScriptRuntimeValidator(scriptRuntime))
	acquisitionState := acquisition.NewCurrentStateStore()
	channels, devices, err := acquisitionService.EnabledConfiguration(context.Background())
	if err != nil {
		slog.Error("load acquisition configuration", "error", err)
		os.Exit(1)
	}
	acquisitionRuntime, err := acquisition.NewRuntimeWithScripts(
		channels,
		devices,
		acquisitionState,
		acquisition.NewModbusSessionFactory(),
		stdlog.New(os.Stderr, "acquisition: ", stdlog.LstdFlags),
		acquisition.RuntimeScriptConfig{
			VersionProvider: acquisitionService.PublishedScriptVersion,
			Executor:        scriptRuntime,
		},
		acquisitionService.EnabledConfiguration,
	)
	if err != nil {
		slog.Error("build acquisition runtime", "error", err)
		os.Exit(1)
	}
	acquisitionService.SetRuntimeRefresher(acquisitionRuntime.Refresh)
	acquisitionService.SetScriptRuntimeStateReader(acquisitionRuntime)

	mqttSecretBox, secretErr := mqtt.NewEnvironmentSecretBox()
	if secretErr != nil && !errors.Is(secretErr, mqtt.ErrMasterSecretRequired) {
		slog.Warn("MQTT master secret unavailable; encrypted credentials cannot be used", "error_type", fmt.Sprintf("%T", secretErr))
	}
	mqttRepository := mqtt.NewRepository(database.GORM, mqttSecretBox)
	mqttConfig, err := mqttRepository.GetConfig(context.Background())
	if err != nil {
		slog.Error("load MQTT configuration", "error", err)
		os.Exit(1)
	}
	mqttTopics, err := mqtt.NewTopicBuilder(mqttConfig.TopicPrefix, mqttConfig.EdgeID)
	if err != nil {
		slog.Error("build MQTT topic configuration", "error_type", fmt.Sprintf("%T", err))
		os.Exit(1)
	}
	mqttRuntime := mqtt.NewRuntime(mqttRepository)
	mqttWorker := mqtt.NewReliableOutboxWorker(mqttRepository, mqttRuntime, mqttConfig.CommandQueueCapacity, stdlog.New(os.Stderr, "mqtt-outbox: ", stdlog.LstdFlags))
	mqttProjector := mqtt.NewLatestStateProjector(mqttRuntime, mqttTopics, time.Duration(mqttConfig.RawPublishIntervalMS)*time.Millisecond)
	mqttEvents := mqtt.NewEventProjector(mqttWorker, mqttTopics, time.Duration(mqttConfig.OutboxRetentionDays)*24*time.Hour)
	acquisitionRuntime.SetCommandQueueConfig(mqttConfig.CommandQueueCapacity, mqttConfig.CommandPollFairness)
	acquisitionState.SetObserver(mqttProjector.OnState)
	mqttProjector.Seed(acquisitionState.List())

	// These sinks are invoked only after acquisition has committed its state
	// and at the channel runner's safe boundary. MQTT callbacks never receive
	// or operate a Modbus session.
	acquisitionRuntime.SetScriptRuntime(acquisition.RuntimeScriptConfig{
		VersionProvider: acquisitionService.PublishedScriptVersion,
		Executor:        scriptRuntime,
		EventSink:       mqttEvents.Emit,
		CycleSink: func(_ context.Context, state acquisition.CurrentState) {
			mqttProjector.OnCycle(state)
		},
	})
	mqttIntake := mqtt.NewCommandIntake(mqttRepository, acquisitionService, acquisitionRuntime, mqttTopics)
	mqttService, err := mqtt.NewService(mqttRepository, mqttRuntime, mqttProjector, mqttEvents, mqttIntake, acquisitionRuntime)
	if err != nil {
		slog.Error("build MQTT service", "error", err)
		os.Exit(1)
	}
	mqttRuntime.SetCallbacks(mqtt.RuntimeCallbacks{
		OnConnected: func(ctx context.Context, runtimeConfig mqtt.RuntimeConfig) {
			builder, builderErr := mqtt.NewTopicBuilder(runtimeConfig.TopicPrefix, runtimeConfig.EdgeID)
			if builderErr != nil {
				slog.Error("configure MQTT projections after connect", "error_type", fmt.Sprintf("%T", builderErr))
				return
			}
			mqttProjector.Configure(builder, time.Duration(runtimeConfig.RawPublishIntervalMS)*time.Millisecond)
			mqttEvents.Configure(builder, time.Duration(runtimeConfig.OutboxRetentionDays)*24*time.Hour)
			mqttIntake.Configure(builder)
			mqttProjector.Republish()
			payload, payloadErr := mqtt.BuildEdgeStatus(runtimeConfig.EdgeID, mqtt.NewMessageID(time.Now().UTC()), time.Now().UTC(), true, "connected")
			if payloadErr != nil {
				slog.Error("build MQTT online status", "error_type", fmt.Sprintf("%T", payloadErr))
				return
			}
			publishContext, cancel := context.WithTimeout(ctx, time.Duration(runtimeConfig.ConnectTimeoutMS)*time.Millisecond)
			defer cancel()
			if publishErr := mqttRuntime.Publish(publishContext, mqtt.Publication{Topic: builder.EdgeStatus(), QoS: 1, Retain: true, Payload: payload}); publishErr != nil {
				slog.Error("publish MQTT online status", "error_type", fmt.Sprintf("%T", publishErr))
			}
		},
		OnMessage: func(topic string, payload []byte) {
			if intakeErr := mqttIntake.Handle(context.Background(), topic, payload); intakeErr != nil {
				slog.Error("handle MQTT command", "error_code", mqtt.StableCommandErrorCode(intakeErr))
			}
		},
	})

	application, err := app.New(*cfg, database, app.Dependencies{
		Acquisition:      acquisitionService,
		AcquisitionState: acquisitionState,
		Auth:             authService,
		RBAC:             rbacService,
		Department:       deptService,
		User:             userService,
		Dictionary:       dictionaryService,
		SysConfig:        configService,
		File:             fileService,
		Log:              logService,
		Notification:     notificationService,
		MQTT:             mqttService,
	})
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
	}

	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	var runtimeWait sync.WaitGroup
	startRuntime := func(name string, run func(context.Context) error) {
		runtimeWait.Add(1)
		go func() {
			defer runtimeWait.Done()
			if runErr := run(runtimeContext); runErr != nil && !errors.Is(runErr, context.Canceled) {
				application.Logger.Error(name+" stopped", "error_type", fmt.Sprintf("%T", runErr))
			}
		}()
	}
	startRuntime("MQTT outbox worker", mqttWorker.Run)
	startRuntime("MQTT latest-state projector", mqttProjector.Run)
	startRuntime("acquisition runtime", acquisitionRuntime.Run)
	startupContext, cancelStartup := context.WithTimeout(runtimeContext, 5*time.Second)
	if startupErr := acquisitionRuntime.WaitStarted(startupContext); startupErr != nil {
		application.Logger.Error("acquisition runtime startup incomplete", "error_type", fmt.Sprintf("%T", startupErr))
	} else if recoveryErr := mqttIntake.Recover(runtimeContext); recoveryErr != nil {
		application.Logger.Error("recover MQTT commands", "error_type", fmt.Sprintf("%T", recoveryErr))
	}
	cancelStartup()
	startRuntime("MQTT runtime", mqttRuntime.Run)

	server := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           application.Router,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	go func() {
		application.Logger.Info("HTTP server started", "address", cfg.HTTP.Address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			application.Logger.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancelRuntime()
	runtimeWait.Wait()

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		application.Logger.Error("HTTP server shutdown failed", "error", err)
		os.Exit(1)
	}
	application.Logger.Info("HTTP server stopped")
}

func newAuthService(database *platformdatabase.Database, jwtConfig config.JWTConfig, guardConfig config.LoginGuardConfig) (*auth.Service, error) {
	tokens, err := auth.NewTokenManager(auth.TokenConfig{
		SigningKey: jwtConfig.Secret,
		Issuer:     jwtConfig.Issuer,
		Audience:   jwtConfig.Audience,
		TTL:        jwtConfig.TTL,
	})
	if err != nil {
		return nil, err
	}
	loginGuard, err := auth.NewLoginGuard(auth.LoginGuardConfig{
		IPWindow:            guardConfig.IPWindow,
		IPMaxAttempts:       guardConfig.IPMaxAttempts,
		UsernameWindow:      guardConfig.UsernameWindow,
		UsernameMaxAttempts: guardConfig.UsernameMaxAttempts,
		BackoffInitial:      guardConfig.BackoffInitial,
		BackoffMax:          guardConfig.BackoffMax,
		LockDuration:        guardConfig.LockDuration,
		MaxEntries:          guardConfig.MaxEntries,
	})
	if err != nil {
		return nil, fmt.Errorf("build login guard: %w", err)
	}
	return auth.NewService(auth.NewRepository(database.GORM), tokens, loginGuard)
}
