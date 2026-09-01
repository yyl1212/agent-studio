package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/bootstrap"
	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := run(signalContext, logger); err != nil {
		observability.Log(context.Background(), logger, slog.LevelError, "worker stopped", observability.IDs{},
			slog.String("error_category", string(observability.ErrorCategoryInternal)))
		os.Exit(1)
	}
}

func run(processContext context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	info := buildinfo.Current()
	startupContext, cancelStartup := context.WithTimeout(processContext, 15*time.Second)
	defer cancelStartup()
	common, err := bootstrap.BuildCommon(startupContext, cfg, info, logger)
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = common.Close(shutdownContext)
	}()
	ownerID := workerOwnerID()
	components, err := bootstrap.BuildWorker(common, ownerID, logger)
	if err != nil {
		return err
	}
	logger.Info("worker ready", "worker_id", ownerID, "max_active_runs", cfg.WorkerMaxActiveRuns)
	workerContext, stopWorker := context.WithCancel(processContext)
	defer stopWorker()
	done := make(chan error, 1)
	go func() { done <- components.Worker.Run(workerContext) }()
	select {
	case err := <-done:
		return err
	case <-processContext.Done():
		stopWorker()
		return <-done
	case <-common.Maintenance.Lost():
		stopWorker()
		<-done
		return errors.New("database maintenance lease lost")
	}
}

func workerOwnerID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s:%d:%s", hostname, os.Getpid(), uuid.NewString())
}
