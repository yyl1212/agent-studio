package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/httpapi"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/apps/api/internal/store/postgres"
	workerprocess "github.com/yyl1212/agent-studio/apps/api/internal/worker"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/database"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type Common struct {
	Config       config.Config
	Info         buildinfo.Info
	Telemetry    *observability.Runtime
	Store        *postgres.Store
	Maintenance  *database.MaintenanceLease
	PoolMetrics  metric.Registration
	Registry     *nodes.Registry
	Compiler     *engine.Compiler
	Cipher       *runpayload.Cipher
	NodePackages *nodeindex.Catalog

	closeOnce sync.Once
	closeErr  error
}

type WorkerComponents struct {
	Common     *Common
	Engine     *engine.Engine
	RunService *workflow.RunService
	Rehydrator *workerprocess.Rehydrator
	Worker     *workerprocess.Worker
}

type APIComponents struct {
	Common        *Common
	Submission    *workflow.RunSubmissionService
	RunService    *workflow.RunService
	RunManagement *workflow.RunManagementService
	RunRecovery   *workflow.RunRecoveryService
	Debug         *workflow.DebugService
	Follower      *workflow.RunEventFollower
	Router        http.Handler
}

func BuildCommon(ctx context.Context, cfg config.Config, info buildinfo.Info, logger *slog.Logger) (_ *Common, err error) {
	if ctx == nil {
		return nil, errors.New("bootstrap context is required")
	}
	cipher, err := runpayload.New(cfg.RunPayloadEncryptionKey)
	if err != nil {
		return nil, errors.New("initialize run payload cipher: invalid configuration")
	}
	telemetry, err := observability.New(ctx, observability.Options{
		Endpoint: cfg.OTelEndpoint, ServiceName: cfg.OTelServiceName, ServiceVersion: info.Version,
		ResourceAttributes: cfg.OTelResourceAttributes, ExportTimeout: cfg.OTelExportTimeout,
		Compression: cfg.OTelCompression, MetricExportInterval: cfg.OTelMetricExportInterval,
	}, logger)
	if err != nil {
		return nil, err
	}
	common := &Common{Config: cfg, Info: info, Telemetry: telemetry, Cipher: cipher}
	defer func() {
		if err != nil {
			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = common.Close(shutdownContext)
		}
	}()
	common.Store, err = postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	common.Maintenance, err = common.Store.PrepareRuntime(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare database runtime: %w", err)
	}
	common.PoolMetrics, err = common.Store.RegisterPoolMetrics(telemetry.Providers())
	if err != nil {
		return nil, err
	}
	indexStore, err := nodeindex.OpenStore(cfg.NodeIndexCacheDir)
	if err != nil {
		return nil, fmt.Errorf("open node index: %w", err)
	}
	common.NodePackages = nodeindex.NewCatalog(indexStore, nodeindex.Runtime{Version: info.Version, NodeAPI: info.APIVersion})
	common.Registry, err = buildRegistry(cfg, info)
	if err != nil {
		return nil, err
	}
	common.Compiler = engine.NewCompiler(common.Registry)
	return common, nil
}

func BuildWorker(common *Common, ownerID string, logger *slog.Logger) (*WorkerComponents, error) {
	if common == nil || common.Store == nil || common.Compiler == nil || common.Cipher == nil || common.Telemetry == nil || ownerID == "" {
		return nil, errors.New("worker bootstrap dependencies are incomplete")
	}
	providers := common.Telemetry.Providers()
	executionEngine := engine.New(engine.Options{MaxParallel: common.Config.MaxParallelNodes, Timeout: common.Config.WorkflowTimeout, Telemetry: providers})
	runService := workflow.NewRunService(common.Store, common.Compiler, executionEngine, workflow.WithLogger(logger), workflow.WithRunTelemetry(providers))
	rehydrator := workerprocess.NewRehydrator(common.Store, common.Compiler, common.Cipher)
	runner := workerprocess.New(durableWorkerConfig(common.Config, ownerID), common.Store, rehydrator, runService, common.Cipher, workerprocess.WithLogger(logger), workerprocess.WithTelemetry(providers))
	return &WorkerComponents{Common: common, Engine: executionEngine, RunService: runService, Rehydrator: rehydrator, Worker: runner}, nil
}

func durableWorkerConfig(cfg config.Config, ownerID string) workerprocess.Config {
	return workerprocess.Config{
		OwnerID:             ownerID,
		MaxActiveRuns:       cfg.WorkerMaxActiveRuns,
		LeaseDuration:       cfg.WorkerLeaseDuration,
		HeartbeatInterval:   cfg.WorkerHeartbeatInterval,
		ClaimInterval:       cfg.WorkerClaimInterval,
		QueueSampleInterval: cfg.WorkerQueueSampleInterval,
		ShutdownTimeout:     cfg.WorkerShutdownTimeout,
	}
}

func BuildAPI(common *Common, logger *slog.Logger) (*APIComponents, error) {
	if common == nil || common.Store == nil || common.Compiler == nil || common.Cipher == nil || common.Telemetry == nil || common.Registry == nil || common.NodePackages == nil {
		return nil, errors.New("API bootstrap dependencies are incomplete")
	}
	providers := common.Telemetry.Providers()
	submission := workflow.NewRunSubmissionService(common.Store, common.Cipher)
	runService := workflow.NewRunService(common.Store, common.Compiler, nil,
		workflow.WithLogger(logger), workflow.WithRunTelemetry(providers), workflow.WithRunSubmission(submission))
	runManagement := workflow.NewQueuedRunManagementService(common.Store, common.Compiler, submission)
	runRecovery := workflow.NewRunRecoveryService(common.Store, common.Compiler)
	debugService := workflow.NewQueuedDebugService(common.Store, common.Compiler, submission)
	follower := workflow.NewRunEventFollower(common.Store, common.Config.RunEventPollInterval)
	agentRuns := workflow.NewQueuedAgentRunService(runService, common.Store)
	router := httpapi.NewRouter(httpapi.Dependencies{
		Registry:           common.Registry,
		Workflows:          workflow.NewService(common.Store, common.Compiler, common.Registry),
		WorkflowManagement: workflow.NewWorkflowManagementService(common.Store),
		VersionGovernance:  workflow.NewVersionGovernanceService(common.Store, common.Compiler, common.Registry),
		RunSubmissions:     runService, RunFollower: follower, Runs: common.Store, AgentRuns: agentRuns,
		RunManagement: runManagement, RunRecovery: runRecovery, RetrySubmissions: runManagement,
		Debugger: debugService, RerunSubmissions: debugService,
		Readiness: common.Store, NodePackages: common.NodePackages, WebOrigin: common.Config.WebOrigin,
		Logger: logger, Telemetry: providers,
	})
	return &APIComponents{
		Common: common, Submission: submission, RunService: runService, RunManagement: runManagement, RunRecovery: runRecovery,
		Debug: debugService, Follower: follower, Router: router,
	}, nil
}

func (common *Common) Close(ctx context.Context) error {
	if common == nil {
		return nil
	}
	common.closeOnce.Do(func() {
		var errs []error
		if common.PoolMetrics != nil {
			errs = append(errs, common.PoolMetrics.Unregister())
		}
		if common.Maintenance != nil {
			errs = append(errs, common.Maintenance.Release(ctx))
		}
		if common.Store != nil {
			common.Store.Close()
		}
		if common.Telemetry != nil {
			errs = append(errs, common.Telemetry.Shutdown(ctx))
		}
		common.closeErr = errors.Join(errs...)
	})
	return common.closeErr
}

func buildRegistry(cfg config.Config, info buildinfo.Info) (*nodes.Registry, error) {
	registry := nodes.NewRegistry()
	provider, defaultModel, err := createModelProvider(cfg)
	if err != nil {
		return nil, err
	}
	record := builtin.RuntimeRecord(info)
	if err := registry.RegisterPackage(record, func(registrar agentnode.Registrar) error {
		for _, register := range []func(agentnode.Registrar) error{
			builtin.RegisterCore,
			func(registrar agentnode.Registrar) error {
				return builtin.RegisterLLM(registrar, provider, defaultModel)
			},
			func(registrar agentnode.Registrar) error {
				return builtin.RegisterIntegrationNodes(registrar, builtin.HTTPOptions{AllowPrivateNetwork: cfg.HTTPNodeAllowPrivate})
			},
		} {
			if err := register(registrar); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("register core package: %w", err)
	}
	if err := generated.RegisterNodes(registry); err != nil {
		return nil, fmt.Errorf("register extension nodes: %w", err)
	}
	return registry, nil
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
		return modelprovider.NewOpenAICompatible(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, &http.Client{Timeout: 65 * time.Second}), cfg.OpenAIDefaultModel, nil
	default:
		return nil, "", fmt.Errorf("unsupported MODEL_PROVIDER %q", cfg.ModelProvider)
	}
}
