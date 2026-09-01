package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/bootstrap"
	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		observability.Log(context.Background(), logger, slog.LevelError, "server stopped", observability.IDs{},
			slog.String("error_category", string(observability.ErrorCategoryInternal)),
		)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	info := buildinfo.Current()
	logBuildInfo(logger, info)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelStartup()
	common, err := bootstrap.BuildCommon(startupContext, cfg, info, logger)
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = common.Close(ctx)
	}()
	api, err := bootstrap.BuildAPI(common, logger)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.HTTPAddr)
		serveErrors <- server.ListenAndServe()
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	serveErr := waitForRuntimeStop(serveErrors, signalContext.Done(), common.Maintenance.Lost())

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownContext)
	closeErr := common.Close(shutdownContext)
	if serveErr != nil {
		return serveErr
	}
	if shutdownErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", shutdownErr)
	}
	return closeErr
}

func waitForRuntimeStop(serveErrors <-chan error, signalDone <-chan struct{}, maintenanceLost <-chan error) error {
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-signalDone:
		return nil
	case <-maintenanceLost:
		return errors.New("database maintenance lease lost")
	}
}

func logBuildInfo(logger *slog.Logger, info buildinfo.Info) {
	logger.Info(
		"server starting",
		"version", info.Version,
		"sdk_version", info.SDKVersion,
		"api_version", info.APIVersion,
		"commit", info.Revision,
		"dirty", info.Dirty,
	)
}
