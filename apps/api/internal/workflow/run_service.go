package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"time"

	"github.com/google/uuid"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var ErrInputValidation = errors.New("run input validation failed")

type PreparedRun struct {
	RunID             string
	Plan              *engine.Plan
	Input             map[string]any
	Mode              domain.RunMode
	WorkflowID        string
	WorkflowVersionID *string
	DraftRevision     *int64
	Scope             *engine.ExecutionScope
	secretRedactor    *SecretRedactor
}

type RunService struct {
	store    Store
	compiler Compiler
	engine   Engine
	logger   *slog.Logger
}

type RunOption func(*RunService)

func WithLogger(logger *slog.Logger) RunOption {
	return func(service *RunService) {
		if logger != nil {
			service.logger = logger
		}
	}
}

func NewRunService(store Store, compiler Compiler, runtime Engine, options ...RunOption) *RunService {
	service := &RunService{
		store:    store,
		compiler: compiler,
		engine:   runtime,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *RunService) PrepareDraft(ctx context.Context, workflowID string, revision int64, input map[string]any) (*PreparedRun, error) {
	workflow, err := service.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if err := ensureWorkflowActive(workflow); err != nil {
		return nil, err
	}
	if workflow.DraftRevision != revision {
		return nil, domain.ErrRevisionConflict
	}
	graph, plan, err := compileRunGraph(service.compiler, workflow.DraftGraph)
	if err != nil {
		return nil, err
	}
	inputSchema, err := deriveInputSchema(graph)
	if err != nil {
		return nil, err
	}
	input = normalizeInput(input)
	if err := validateInput(inputSchema, input); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	inputJSON, inputPaths, secretRedactor, err := persistedRunInput(input)
	if err != nil {
		return nil, fmt.Errorf("encode run input: %w", err)
	}
	run := domain.Run{
		ID:                 runID,
		WorkflowID:         workflow.ID,
		DraftRevision:      &revision,
		GraphSnapshot:      append(json.RawMessage(nil), workflow.DraftGraph...),
		Mode:               domain.RunModeTest,
		Status:             domain.RunRunning,
		Input:              inputJSON,
		InputRedactedPaths: inputPaths,
		StartedAt:          time.Now().UTC(),
	}
	if err := service.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return &PreparedRun{
		RunID:          runID,
		Plan:           plan,
		Input:          input,
		Mode:           domain.RunModeTest,
		WorkflowID:     workflow.ID,
		DraftRevision:  &revision,
		secretRedactor: secretRedactor,
	}, nil
}

func (service *RunService) PrepareAgent(ctx context.Context, slug, workflowVersionID string, input map[string]any) (*PreparedRun, error) {
	workflow, version, err := service.store.GetAgentVersion(ctx, slug, workflowVersionID)
	if err != nil {
		return nil, err
	}
	if err := ensureWorkflowActive(workflow); err != nil {
		return nil, err
	}
	_, plan, err := compileRunGraph(service.compiler, version.Graph)
	if err != nil {
		return nil, err
	}
	input = normalizeInput(input)
	if err := validateInput(version.InputSchema, input); err != nil {
		return nil, err
	}
	runID := uuid.NewString()
	inputJSON, inputPaths, secretRedactor, err := persistedRunInput(input)
	if err != nil {
		return nil, fmt.Errorf("encode run input: %w", err)
	}
	versionID := version.ID
	run := domain.Run{
		ID:                 runID,
		WorkflowID:         workflow.ID,
		WorkflowVersionID:  &versionID,
		Mode:               domain.RunModePublished,
		Status:             domain.RunRunning,
		Input:              inputJSON,
		InputRedactedPaths: inputPaths,
		StartedAt:          time.Now().UTC(),
	}
	if err := service.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return &PreparedRun{
		RunID:             runID,
		Plan:              plan,
		Input:             input,
		Mode:              domain.RunModePublished,
		WorkflowID:        workflow.ID,
		WorkflowVersionID: &versionID,
		secretRedactor:    secretRedactor,
	}, nil
}

func persistedRunInput(input map[string]any) (json.RawMessage, []string, *SecretRedactor, error) {
	report := RedactWithReport(input)
	encoded, err := json.Marshal(report.Value)
	if err != nil {
		return nil, nil, nil, err
	}
	paths := append([]string{}, report.Paths...)
	return encoded, paths, NewSecretRedactor(report.SecretValues), nil
}

func (service *RunService) Execute(ctx context.Context, prepared *PreparedRun, observer engine.Observer) (engine.RunResult, error) {
	persistence := &persistenceObserver{
		store:      service.store,
		prepared:   prepared,
		downstream: observer,
		started:    make(map[string]time.Time),
	}
	var result engine.RunResult
	var runErr error
	if prepared.Scope == nil {
		result, runErr = service.engine.Run(ctx, prepared.RunID, prepared.Plan, prepared.Input, persistence)
	} else {
		result, runErr = service.engine.RunWithScope(ctx, prepared.RunID, prepared.Plan, prepared.Input, persistence, *prepared.Scope)
	}
	if runErr != nil {
		service.logRunError(prepared.RunID, runErr)
	}
	status := domain.RunCompleted
	var publicError *domain.PublicError
	if runErr != nil {
		status = domain.RunFailed
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			status = domain.RunCancelled
		}
		var executionErr *engine.NodeExecutionError
		if errors.As(runErr, &executionErr) {
			nodeType, nodeVersion := "", ""
			if prepared != nil && prepared.Plan != nil {
				if compiled, ok := prepared.Plan.Nodes[executionErr.NodeID]; ok {
					nodeType = compiled.Node.Type
					nodeVersion = compiled.Node.TypeVersion
				}
			}
			publicError = domain.NewPublicNodeError(executionErr.Err, executionErr.NodeID, nodeType, nodeVersion)
		} else {
			publicError = domain.NewPublicRunError(runErr)
		}
	}
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finishErr := service.store.FinishRun(finishContext, prepared.RunID, status, Redact(result.Output), publicError, result.EndedAt)
	if runErr != nil {
		return result, runErr
	}
	if finishErr != nil {
		return result, finishErr
	}
	return result, nil
}

func (service *RunService) logRunError(runID string, err error) {
	var executionErr *engine.NodeExecutionError
	if !errors.As(err, &executionErr) {
		return
	}
	kind := agentnode.KindOf(executionErr.Err)
	code := "execution_failed"
	var details map[string]any
	var nodeErr *agentnode.NodeError
	if errors.As(executionErr.Err, &nodeErr) {
		code = nodeErr.Code
		details, _ = Redact(nodeErr.Details).(map[string]any)
	}
	service.logger.Error(
		"node execution failed",
		"run_id", runID,
		"node_id", executionErr.NodeID,
		"node_type", executionErr.NodeType,
		"error_kind", kind,
		"error_code", code,
		"error_causes", errorCauseTypes(err),
		"error_details", details,
	)
}

func errorCauseTypes(err error) []string {
	causes := make([]string, 0, 4)
	for current := err; current != nil; current = errors.Unwrap(current) {
		causes = append(causes, reflect.TypeOf(current).String())
	}
	return causes
}

func compileRunGraph(compiler Compiler, raw json.RawMessage) (domain.Graph, *engine.Plan, error) {
	var graph domain.Graph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return domain.Graph{}, nil, &ValidationError{Issues: []domain.ValidationIssue{{Code: "GRAPH_JSON_INVALID", Message: "工作流图 JSON 无效"}}}
	}
	plan, issues := compiler.Compile(graph)
	if len(issues) > 0 {
		return domain.Graph{}, nil, &ValidationError{Issues: issues}
	}
	return graph, plan, nil
}

func validateInput(schemaJSON json.RawMessage, input map[string]any) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("%w: invalid input schema", ErrInputValidation)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "urn:agent-studio:run-input"
	if err := compiler.AddResource(resource, document); err != nil {
		return fmt.Errorf("%w: invalid input schema", ErrInputValidation)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return fmt.Errorf("%w: invalid input schema", ErrInputValidation)
	}
	if err := compiled.Validate(input); err != nil {
		return fmt.Errorf("%w: %v", ErrInputValidation, err)
	}
	return nil
}

func normalizeInput(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return input
}

type persistenceObserver struct {
	store      Store
	prepared   *PreparedRun
	downstream engine.Observer
	started    map[string]time.Time
}

func (observer *persistenceObserver) Observe(ctx context.Context, event engine.Event) error {
	safeEvent := event
	inputReport := RedactWithReport(event.Input)
	outputReport := RedactWithReport(event.Output)
	safeEvent.Input = inputReport.Value
	safeEvent.Output = outputReport.Value
	safeEvent.InputRedactedPaths = append([]string(nil), inputReport.Paths...)
	safeEvent.OutputRedactedPaths = append([]string(nil), outputReport.Paths...)
	if safeEvent.ActivePorts == nil {
		safeEvent.ActivePorts = []string{}
	}
	if safeEvent.InputRedactedPaths == nil {
		safeEvent.InputRedactedPaths = []string{}
	}
	if safeEvent.OutputRedactedPaths == nil {
		safeEvent.OutputRedactedPaths = []string{}
	}
	inputJSON, err := marshalEventValue(safeEvent.Input)
	if err != nil {
		return err
	}
	outputJSON, err := marshalEventValue(safeEvent.Output)
	if err != nil {
		return err
	}
	errorJSON, err := marshalEventValue(safeEvent.Error)
	if err != nil {
		return err
	}
	activePortsJSON, err := json.Marshal(safeEvent.ActivePorts)
	if err != nil {
		return fmt.Errorf("encode active ports: %w", err)
	}
	inputPathsJSON, err := json.Marshal(safeEvent.InputRedactedPaths)
	if err != nil {
		return fmt.Errorf("encode input redaction paths: %w", err)
	}
	outputPathsJSON, err := json.Marshal(safeEvent.OutputRedactedPaths)
	if err != nil {
		return fmt.Errorf("encode output redaction paths: %w", err)
	}
	if len(inputJSON) > 1<<20 || len(outputJSON) > 1<<20 || len(errorJSON) > 64<<10 {
		return domain.ErrRunEventBudgetExceeded
	}
	runEvent := domain.RunEvent{
		RunID: safeEvent.RunID, Sequence: safeEvent.Sequence, Type: safeEvent.Type, NodeID: safeEvent.NodeID,
		Status: safeEvent.Status, Input: inputJSON, Output: outputJSON, ActivePorts: append([]string(nil), safeEvent.ActivePorts...),
		Error: safeEvent.Error, InputRedactedPaths: append([]string(nil), safeEvent.InputRedactedPaths...),
		OutputRedactedPaths: append([]string(nil), safeEvent.OutputRedactedPaths...), Timestamp: safeEvent.Timestamp,
		DataBytes: int64(len(inputJSON) + len(outputJSON) + len(errorJSON) + len(activePortsJSON) + len(inputPathsJSON) + len(outputPathsJSON)),
	}
	var nodeRun *domain.NodeRun
	if event.NodeID != "" {
		built, err := observer.nodeRunForEvent(safeEvent, inputJSON, outputJSON)
		if err != nil {
			return err
		}
		nodeRun = &built
	}
	budget := domain.RunEventBudget{MaxEvents: 2*len(observer.prepared.Plan.Nodes) + 2, MaxTotalDataBytes: 16 << 20}
	if err := observer.store.PersistRunEvent(ctx, runEvent, nodeRun, budget); err != nil {
		return err
	}
	if observer.downstream != nil {
		return observer.downstream.Observe(ctx, safeEvent)
	}
	return nil
}

func (observer *persistenceObserver) nodeRunForEvent(event engine.Event, inputJSON, outputJSON json.RawMessage) (domain.NodeRun, error) {
	compiled, exists := observer.prepared.Plan.Nodes[event.NodeID]
	if !exists {
		return domain.NodeRun{}, fmt.Errorf("event references unknown node %s", event.NodeID)
	}
	var startedAt, endedAt *time.Time
	if event.Status == domain.NodeRunning {
		started := event.Timestamp
		observer.started[event.NodeID] = started
		startedAt = &started
	} else {
		if started, ok := observer.started[event.NodeID]; ok {
			startedCopy := started
			startedAt = &startedCopy
		}
		ended := event.Timestamp
		endedAt = &ended
	}
	return domain.NodeRun{
		ID:        uuid.NewString(),
		RunID:     observer.prepared.RunID,
		NodeID:    event.NodeID,
		NodeType:  compiled.Node.Type,
		Status:    event.Status,
		Input:     inputJSON,
		Output:    outputJSON,
		Error:     event.Error,
		StartedAt: startedAt,
		EndedAt:   endedAt,
	}, nil
}

func marshalEventValue(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode run event: %w", err)
	}
	return encoded, nil
}
