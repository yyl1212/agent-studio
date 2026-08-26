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
	"github.com/yyl1212/agent-studio/internal/nodeindex"
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
	indexCatalog, err := openNodePackageCatalog(cfg.NodeIndexCacheDir, info, nodeindex.OpenStore)
	if err != nil {
		return err
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
	processContext, stopCoordinator := context.WithCancel(context.Background())
	runCoordinator := workflow.NewRunCoordinator(store, workflow.WithCoordinatorLogger(logger))
	workflowService := workflow.NewService(store, compiler, registry)
	workflowManagement := workflow.NewWorkflowManagementService(store)
	versionGovernance := workflow.NewVersionGovernanceService(store, compiler, registry)
	runService := workflow.NewRunService(store, compiler, runtime, workflow.WithLogger(logger), workflow.WithRunCoordinator(runCoordinator))
	agentRunSupervisor := workflow.NewAgentRunSupervisor(processContext, cfg.MaxActiveAgentRuns, runService, workflow.WithAgentRunSupervisorLogger(logger))
	agentRunService := workflow.NewAgentRunService(runService, store, agentRunSupervisor, runCoordinator)
	runManagement := workflow.NewRunManagementService(store, compiler, runCoordinator)
	debugService := workflow.NewDebugService(store, compiler)
	router := httpapi.NewRouter(httpapi.Dependencies{
		Registry:           registry,
		Workflows:          workflowService,
		WorkflowManagement: workflowManagement,
		VersionGovernance:  versionGovernance,
		Runner:             runService,
		Runs:               store,
		AgentRuns:          agentRunService,
		RunManagement:      runManagement,
		Debugger:           debugService,
		Readiness:          store,
		NodePackages:       indexCatalog,
		WebOrigin:          cfg.WebOrigin,
		Logger:             logger,
	})
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	coordinatorDone := make(chan error, 1)
	go func() {
		coordinatorDone <- runCoordinator.Run(processContext)
	}()

	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.HTTPAddr)
		serveErrors <- server.ListenAndServe()
	}()

	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	var serveErr error
	select {
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("serve HTTP: %w", err)
		}
	case <-signalContext.Done():
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	shutdownErr := shutdownRuntime(shutdownContext, agentRunSupervisor, runCoordinator, server.Shutdown, stopCoordinator, coordinatorDone)
	if serveErr != nil {
		return serveErr
	}
	return shutdownErr
}

type shutdownCoordinator interface {
	BeginShutdown()
}

type shutdownAgentSupervisor interface {
	BeginShutdown()
	Wait(context.Context) error
}

func shutdownRuntime(
	ctx context.Context,
	supervisor shutdownAgentSupervisor,
	coordinator shutdownCoordinator,
	shutdownHTTP func(context.Context) error,
	stopCoordinator context.CancelFunc,
	coordinatorDone <-chan error,
) error {
	supervisor.BeginShutdown()
	coordinator.BeginShutdown()
	httpErr := shutdownHTTP(ctx)
	supervisorErr := supervisor.Wait(ctx)
	stopCoordinator()
	wait := time.NewTimer(5 * time.Second)
	defer wait.Stop()
	var coordinatorErr error
	select {
	case coordinatorErr = <-coordinatorDone:
	case <-ctx.Done():
		coordinatorErr = ctx.Err()
	case <-wait.C:
		coordinatorErr = errors.New("run coordinator shutdown timed out")
	}
	if httpErr != nil {
		return fmt.Errorf("shutdown HTTP server: %w", httpErr)
	}
	if supervisorErr != nil {
		return fmt.Errorf("shutdown agent run supervisor: %w", supervisorErr)
	}
	if coordinatorErr != nil {
		return fmt.Errorf("shutdown run coordinator: %w", coordinatorErr)
	}
	return nil
}

type nodeIndexStoreOpener func(string) (*nodeindex.Store, error)

func openNodePackageCatalog(cacheDir string, info buildinfo.Info, openStore nodeIndexStoreOpener) (httpapi.NodePackageCatalog, error) {
	store, err := openStore(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("open node index: %w", err)
	}
	return nodeindex.NewCatalog(store, nodeindex.Runtime{Version: info.Version, NodeAPI: info.APIVersion}), nil
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
