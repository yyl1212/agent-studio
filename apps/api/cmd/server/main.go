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
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
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
	telemetry, store, err := openRuntimeAndStore(
		startupContext, cfg, info, logger,
		func(ctx context.Context, options observability.Options, logger *slog.Logger) (telemetryRuntime, error) {
			return observability.New(ctx, options, logger)
		},
		postgres.Open,
		defaultTelemetryShutdownContext,
	)
	if err != nil {
		return err
	}
	databaseClosed := false
	var maintenance maintenanceReleaser
	defer func() {
		if !databaseClosed {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeDatabaseRuntime(ctx, logger, maintenance, store)
		}
	}()
	defer shutdownTelemetry(logger, telemetry, defaultTelemetryShutdownContext)
	maintenance, err = store.PrepareRuntime(startupContext)
	if err != nil {
		return fmt.Errorf("prepare database runtime: %w", err)
	}
	poolMetrics, err := store.RegisterPoolMetrics(telemetry.Providers())
	if err != nil {
		return err
	}
	poolMetricsUnregistered := false
	defer func() {
		if !poolMetricsUnregistered {
			_ = poolMetrics.Unregister()
		}
	}()
	indexCatalog, err := openNodePackageCatalog(cfg.NodeIndexCacheDir, info, nodeindex.OpenStore)
	if err != nil {
		return err
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
	providers := telemetry.Providers()
	executionEngine := engine.New(engine.Options{MaxParallel: cfg.MaxParallelNodes, Timeout: cfg.WorkflowTimeout, Telemetry: providers})
	processContext, stopCoordinator := context.WithCancel(context.Background())
	runCoordinator := workflow.NewRunCoordinator(store, workflow.WithCoordinatorLogger(logger))
	workflowService := workflow.NewService(store, compiler, registry)
	workflowManagement := workflow.NewWorkflowManagementService(store)
	versionGovernance := workflow.NewVersionGovernanceService(store, compiler, registry)
	runService := workflow.NewRunService(store, compiler, executionEngine, workflow.WithLogger(logger), workflow.WithRunCoordinator(runCoordinator), workflow.WithRunTelemetry(providers))
	agentRunSupervisor := workflow.NewAgentRunSupervisor(processContext, cfg.MaxActiveAgentRuns, runService, workflow.WithAgentRunSupervisorLogger(logger), workflow.WithAgentRunSupervisorTelemetry(providers))
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
		Telemetry:          providers,
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
	serveErr := waitForRuntimeStop(serveErrors, signalContext.Done(), maintenance.Lost())

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	shutdownErr := shutdownRuntime(
		shutdownContext, logger, agentRunSupervisor, runCoordinator, server.Shutdown, stopCoordinator, coordinatorDone,
		poolMetrics, telemetry, maintenance, store, defaultTelemetryShutdownContext,
	)
	poolMetricsUnregistered = true
	databaseClosed = true
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

type poolMetricRegistration interface {
	Unregister() error
}

type telemetryRuntime interface {
	Providers() observability.Providers
	Shutdown(context.Context) error
}

type storeCloser interface {
	Close()
}

type maintenanceReleaser interface {
	Release(context.Context) error
	Lost() <-chan error
}

type telemetryShutdownContextFactory func() (context.Context, context.CancelFunc)

type telemetryRuntimeFactory func(context.Context, observability.Options, *slog.Logger) (telemetryRuntime, error)
type postgresStoreOpener func(context.Context, string) (*postgres.Store, error)

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

func defaultTelemetryShutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func openRuntimeAndStore(
	ctx context.Context,
	cfg config.Config,
	info buildinfo.Info,
	logger *slog.Logger,
	newRuntime telemetryRuntimeFactory,
	openStore postgresStoreOpener,
	shutdownContext telemetryShutdownContextFactory,
) (telemetryRuntime, *postgres.Store, error) {
	runtime, err := newRuntime(ctx, observability.Options{
		Endpoint: cfg.OTelEndpoint, ServiceName: cfg.OTelServiceName, ServiceVersion: info.Version,
		ResourceAttributes: cfg.OTelResourceAttributes, ExportTimeout: cfg.OTelExportTimeout,
		Compression: cfg.OTelCompression, MetricExportInterval: cfg.OTelMetricExportInterval,
	}, logger)
	if err != nil {
		return nil, nil, err
	}
	store, err := openStore(ctx, cfg.DatabaseURL)
	if err != nil {
		shutdownTelemetry(logger, runtime, shutdownContext)
		return nil, nil, err
	}
	return runtime, store, nil
}

func shutdownTelemetry(logger *slog.Logger, runtime telemetryRuntime, contextFactory telemetryShutdownContextFactory) {
	if runtime == nil {
		return
	}
	ctx, cancel := contextFactory()
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		observability.Log(ctx, logger, slog.LevelError, "telemetry shutdown failed", observability.IDs{},
			slog.String("component", "telemetry"),
			slog.String("reason", "shutdown"),
			slog.String("error_category", string(observability.ErrorCategoryInternal)),
		)
	}
}

func shutdownRuntime(
	ctx context.Context,
	logger *slog.Logger,
	supervisor shutdownAgentSupervisor,
	coordinator shutdownCoordinator,
	shutdownHTTP func(context.Context) error,
	stopCoordinator context.CancelFunc,
	coordinatorDone <-chan error,
	poolMetrics poolMetricRegistration,
	telemetry telemetryRuntime,
	maintenance maintenanceReleaser,
	store storeCloser,
	telemetryContext telemetryShutdownContextFactory,
) error {
	supervisor.BeginShutdown()
	coordinator.BeginShutdown()
	stopCoordinator()
	httpErr := shutdownHTTP(ctx)
	supervisorErr := supervisor.Wait(ctx)
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
	if poolMetrics != nil {
		if err := poolMetrics.Unregister(); err != nil {
			observability.Log(ctx, logger, slog.LevelError, "pool metrics unregister failed", observability.IDs{},
				slog.String("component", "postgres_pool_metrics"),
				slog.String("reason", "shutdown"),
				slog.String("error_category", string(observability.ErrorCategoryInternal)),
			)
		}
	}
	shutdownTelemetry(logger, telemetry, telemetryContext)
	closeDatabaseRuntime(ctx, logger, maintenance, store)
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

func closeDatabaseRuntime(ctx context.Context, logger *slog.Logger, maintenance maintenanceReleaser, store storeCloser) {
	if maintenance != nil {
		if err := maintenance.Release(ctx); err != nil {
			observability.Log(ctx, logger, slog.LevelError, "database maintenance release failed", observability.IDs{},
				slog.String("component", "database_maintenance"),
				slog.String("reason", "shutdown"),
				slog.String("error_category", string(observability.ErrorCategoryInternal)),
			)
		}
	}
	if store != nil {
		store.Close()
	}
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
