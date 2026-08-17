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
	"github.com/yyl1212/agent-studio/apps/api/internal/httpapi"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/store/postgres"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
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
	if err := builtin.RegisterCore(registry); err != nil {
		return fmt.Errorf("register core nodes: %w", err)
	}
	provider, defaultModel, err := createModelProvider(cfg)
	if err != nil {
		return err
	}
	if err := builtin.RegisterLLM(registry, provider, defaultModel); err != nil {
		return fmt.Errorf("register llm node: %w", err)
	}
	if err := builtin.RegisterIntegrationNodes(registry, builtin.HTTPOptions{AllowPrivateNetwork: cfg.HTTPNodeAllowPrivate}); err != nil {
		return fmt.Errorf("register integration nodes: %w", err)
	}

	compiler := engine.NewCompiler(registry)
	runtime := engine.New(engine.Options{MaxParallel: cfg.MaxParallelNodes, Timeout: cfg.WorkflowTimeout})
	workflowService := workflow.NewService(store, compiler)
	runService := workflow.NewRunService(store, compiler, runtime)
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
