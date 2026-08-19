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

	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/httpapi"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/store/postgres"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
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
	store, err := postgres.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(startupContext); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	registry := nodes.NewRegistry()
	provider, defaultModel, err := createModelProvider(cfg)
	if err != nil {
		return err
	}
	if err := registerCorePackage(registry, builtin.RuntimeRecord(info),
		builtin.RegisterCore,
		func(registrar agentnode.Registrar) error {
			return builtin.RegisterLLM(registrar, provider, defaultModel)
		},
		func(registrar agentnode.Registrar) error {
			return builtin.RegisterIntegrationNodes(registrar, builtin.HTTPOptions{AllowPrivateNetwork: cfg.HTTPNodeAllowPrivate})
		},
	); err != nil {
		return err
	}
	if err := registerExtensionNodes(registry); err != nil {
		return fmt.Errorf("register extension nodes: %w", err)
	}

	compiler := engine.NewCompiler(registry)
	runtime := engine.New(engine.Options{MaxParallel: cfg.MaxParallelNodes, Timeout: cfg.WorkflowTimeout})
	workflowService := workflow.NewService(store, compiler, registry)
	runService := workflow.NewRunService(store, compiler, runtime, workflow.WithLogger(logger))
	router := httpapi.NewRouter(httpapi.Dependencies{
		Registry:  registry,
		Workflows: workflowService,
		Runner:    runService,
		Runs:      store,
		Readiness: store,
		WebOrigin: cfg.WebOrigin,
		Logger:    logger,
	})
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
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
	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-signalContext.Done():
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	return nil
}

func registerCorePackage(registry *nodes.Registry, record nodepackage.RuntimeRecord, registrations ...func(agentnode.Registrar) error) error {
	err := registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		for _, register := range registrations {
			if err := register(registrar); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("register core package: %w", err)
	}
	return nil
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

func registerExtensionNodes(registry *nodes.Registry) error {
	return generated.RegisterNodes(registry)
}

func createModelProvider(cfg config.Config) (modelprovider.Provider, string, error) {
	switch cfg.ModelProvider {
	case "mock":
		defaultModel := cfg.OpenAIDefaultModel
		if defaultModel == "" {
			defaultModel = "mock"
		}
		return modelprovider.NewMock(), defaultModel, nil
	case "openai-compatible":
		client := &http.Client{Timeout: 65 * time.Second}
		return modelprovider.NewOpenAICompatible(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, client), cfg.OpenAIDefaultModel, nil
	default:
		return nil, "", fmt.Errorf("unsupported MODEL_PROVIDER %q", cfg.ModelProvider)
	}
}
