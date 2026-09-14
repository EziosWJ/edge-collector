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
	"syscall"

	_ "github.com/EziosWJ/edge-collector/edge-collector-api/docs"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/acquisition"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/app"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/auth"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/config"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/dept"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/dictionary"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/filemgmt"
	"github.com/EziosWJ/edge-collector/edge-collector-api/internal/logmgmt"
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
	acquisitionState := acquisition.NewCurrentStateStore()
	channels, devices, err := acquisitionService.EnabledConfiguration(context.Background())
	if err != nil {
		slog.Error("load acquisition configuration", "error", err)
		os.Exit(1)
	}
	acquisitionRuntime, err := acquisition.NewRuntime(channels, devices, acquisitionState, acquisition.NewModbusSessionFactory(), stdlog.New(os.Stderr, "acquisition: ", stdlog.LstdFlags))
	if err != nil {
		slog.Error("build acquisition runtime", "error", err)
		os.Exit(1)
	}

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
	})
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
	}

	runtimeContext, cancelRuntime := context.WithCancel(context.Background())
	runtimeDone := make(chan struct{})
	go func() {
		defer close(runtimeDone)
		if err := acquisitionRuntime.Run(runtimeContext); err != nil && !errors.Is(err, context.Canceled) {
			application.Logger.Error("acquisition runtime stopped", "error", err)
		}
	}()

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
	<-runtimeDone

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
