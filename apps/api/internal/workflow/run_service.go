package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
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
	WorkflowVersion   int
	DraftRevision     *int64
	StartedAt         time.Time
	Scope             *engine.ExecutionScope
	secretRedactor    *SecretRedactor
	sourceRunID       string
	sourceNodeID      string
	retryOfRunID      string
}

type RunService struct {
	store       Store
	compiler    Compiler
	engine      Engine
	logger      *slog.Logger
	coordinator RunExecutionCoordinator
	telemetry   *runTelemetry
}

type RunOption func(*RunService)

func WithLogger(logger *slog.Logger) RunOption {
	return func(service *RunService) {
		if logger != nil {
			service.logger = logger
		}
	}
}

func WithRunCoordinator(coordinator RunExecutionCoordinator) RunOption {
	return func(service *RunService) {
		service.coordinator = coordinator
	}
}

func WithRunTelemetry(providers observability.Providers) RunOption {
	return func(service *RunService) {
		service.telemetry = newRunTelemetry(providers)
	}
}

func NewRunService(store Store, compiler Compiler, runtime Engine, options ...RunOption) *RunService {
	service := &RunService{
		store:     store,
		compiler:  compiler,
		engine:    runtime,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		telemetry: newRunTelemetry(observability.Providers{}),
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
	prepared, run, err := service.prepareAgent(ctx, slug, workflowVersionID, nil, input)
	if err != nil {
		return nil, err
	}
	if err := service.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (service *RunService) PrepareAgentOnce(ctx context.Context, slug, workflowVersionID, requestKey string, input map[string]any) (*PreparedRun, bool, error) {
	prepared, run, err := service.prepareAgent(ctx, slug, workflowVersionID, &requestKey, input)
	if err != nil {
		return nil, false, err
	}
	stored, created, err := service.store.CreateAgentRun(ctx, run)
	if err != nil {
		return nil, false, err
	}
	if !created {
		return nil, false, nil
	}
	prepared.RunID = stored.ID
	return prepared, true, nil
}

func (service *RunService) prepareAgent(ctx context.Context, slug, workflowVersionID string, requestKey *string, input map[string]any) (*PreparedRun, domain.Run, error) {
	workflow, version, err := service.store.GetAgentVersion(ctx, slug, workflowVersionID)
	if err != nil {
		return nil, domain.Run{}, err
	}
	if err := ensureWorkflowActive(workflow); err != nil {
		return nil, domain.Run{}, err
	}
	_, plan, err := compileRunGraph(service.compiler, version.Graph)
	if err != nil {
		return nil, domain.Run{}, err
	}
	input = normalizeInput(input)
	if err := validateInput(version.InputSchema, input); err != nil {
		return nil, domain.Run{}, err
	}
	runID := uuid.NewString()
	inputJSON, inputPaths, secretRedactor, err := persistedRunInput(input)
	if err != nil {
		return nil, domain.Run{}, fmt.Errorf("encode run input: %w", err)
	}
	versionID := version.ID
	startedAt := time.Now().UTC()
	run := domain.Run{
		ID:                 runID,
		WorkflowID:         workflow.ID,
		WorkflowVersionID:  &versionID,
		AgentRequestKey:    requestKey,
		Mode:               domain.RunModePublished,
		Status:             domain.RunRunning,
		Input:              inputJSON,
		InputRedactedPaths: inputPaths,
		StartedAt:          startedAt,
	}
	return &PreparedRun{
		RunID:             runID,
		Plan:              plan,
		Input:             input,
		Mode:              domain.RunModePublished,
		WorkflowID:        workflow.ID,
		WorkflowVersionID: &versionID,
		WorkflowVersion:   version.Version,
		StartedAt:         startedAt,
		secretRedactor:    secretRedactor,
	}, run, nil
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

// LoadPreparedExecution rebuilds executable in-memory state for an existing
// durable run. It never inserts or mutates a run record.
func LoadPreparedExecution(ctx context.Context, store Store, compiler Compiler, run domain.Run, input map[string]any) (*PreparedRun, error) {
	if store == nil || compiler == nil {
		return nil, errors.New("load prepared execution: dependencies are incomplete")
	}
	_, _, plan, err := loadRunGraphData(ctx, store, compiler, run)
	if err != nil {
		return nil, err
	}
	input = normalizeInput(input)
	_, _, redactor, err := persistedRunInput(input)
	if err != nil {
		return nil, fmt.Errorf("rebuild run input redactor: %w", err)
	}
	prepared := &PreparedRun{
		RunID: run.ID, Plan: plan, Input: input, Mode: run.Mode, WorkflowID: run.WorkflowID,
		WorkflowVersionID: cloneStringPointer(run.WorkflowVersionID), DraftRevision: cloneInt64Pointer(run.DraftRevision),
		StartedAt: run.StartedAt, secretRedactor: redactor,
	}
	if run.Mode == domain.RunModePublished && run.WorkflowVersionID != nil {
		workflow, err := store.GetWorkflow(ctx, run.WorkflowID)
		if err != nil {
			return nil, err
		}
		_, version, err := store.GetAgentVersion(ctx, workflow.Slug, *run.WorkflowVersionID)
		if err != nil {
			return nil, err
		}
		prepared.WorkflowVersion = version.Version
	}
	return prepared, nil
}

func (service *RunService) Execute(ctx context.Context, prepared *PreparedRun, observer engine.Observer) (engine.RunResult, error) {
	executionContext, finishTelemetry := service.telemetry.start(ctx, prepared)
	telemetryStatus := domain.RunFailed
	telemetryCategory := observability.ErrorCategoryInternal
	defer func() { finishTelemetry(telemetryStatus, telemetryCategory) }()
	release := func() {}
	if service.coordinator != nil {
		executionContext, release = service.coordinator.Register(executionContext, prepared.RunID)
	}
	defer release()
	persistence := &persistenceObserver{
		store:      service.store,
		prepared:   prepared,
		downstream: observer,
		started:    make(map[string]time.Time),
	}
	var result engine.RunResult
	var runErr error
	if prepared.Scope == nil {
		result, runErr = service.engine.Run(executionContext, prepared.RunID, prepared.Plan, prepared.Input, persistence)
	} else {
		result, runErr = service.engine.RunWithScope(executionContext, prepared.RunID, prepared.Plan, prepared.Input, persistence, *prepared.Scope)
	}
	if result.EndedAt.IsZero() {
		result.EndedAt = time.Now().UTC()
	}
	result.Output = redactPreparedValue(prepared, result.Output).Value
	if runErr != nil {
		service.logRunError(executionContext, prepared, runErr)
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
	telemetryStatus = status
	telemetryCategory = classifyRunError(runErr)
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(executionContext), 5*time.Second)
	defer cancel()
	if persistence.terminal == nil {
		terminalType := "run.completed"
		if status == domain.RunFailed {
			terminalType = "run.failed"
		} else if status == domain.RunCancelled {
			terminalType = "run.cancelled"
		}
		if err := persistence.Observe(finishContext, engine.Event{
			Sequence: persistence.lastSequence + 1, Type: terminalType, RunID: prepared.RunID,
			Output: result.Output, Error: publicError, Timestamp: result.EndedAt,
		}); err != nil {
			if runErr != nil {
				return result, runErr
			}
			telemetryCategory = observability.ErrorCategoryPersistence
			return result, err
		}
	}
	finalization := RunFinalization{
		RunID: prepared.RunID, Status: status, Output: result.Output, Error: publicError, EndedAt: result.EndedAt,
		TerminalEvent: cloneRunEvent(*persistence.terminal), Budget: persistence.budget(),
	}
	finalEvent, finishErr := service.store.FinalizeRun(finishContext, finalization)
	if finishErr == nil && observer != nil {
		finishErr = observer.Observe(finishContext, runEventToEngineEvent(finalEvent))
	}
	if runErr != nil {
		return result, runErr
	}
	if finishErr != nil {
		telemetryCategory = observability.ErrorCategoryPersistence
		return result, finishErr
	}
	telemetryCategory = ""
	return result, nil
}

func (service *RunService) logRunError(ctx context.Context, prepared *PreparedRun, err error) {
	var executionErr *engine.NodeExecutionError
	if !errors.As(err, &executionErr) {
		return
	}
	kind := safeErrorKind(agentnode.KindOf(executionErr.Err))
	code := "execution_failed"
	var nodeErr *agentnode.NodeError
	if errors.As(executionErr.Err, &nodeErr) {
		code = safeErrorCode(nodeErr.Code)
	}
	observability.Log(ctx, service.logger, slog.LevelError,
		"node execution failed",
		observability.IDs{RunID: preparedRunID(prepared), NodeID: executionErr.NodeID},
		slog.String("node_type", executionErr.NodeType),
		slog.String("error_category", string(observability.ErrorCategoryNodeExecution)),
		slog.String("error_kind", string(kind)),
		slog.String("error_code", code),
	)
}

func safeErrorCode(code string) string {
	switch code {
	case "invalid_config", "invalid_input", "missing_input", "invalid_query", "invalid_text",
		"invalid_body", "webhook_rejected", "missing_webhook_configuration", "upstream_failed",
		"execution_failed", "run_canceled", "upstream_timeout", "upstream_unavailable",
		"model_structured_output_rejected", "model_provider_auth_failed", "model_refused", "model_output_invalid":
		return code
	default:
		return "execution_failed"
	}
}

func safeErrorKind(kind agentnode.ErrorKind) agentnode.ErrorKind {
	switch kind {
	case agentnode.ErrorKindConfig, agentnode.ErrorKindInput, agentnode.ErrorKindTemporary,
		agentnode.ErrorKindCanceled, agentnode.ErrorKindInternal:
		return kind
	default:
		return agentnode.ErrorKindInternal
	}
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
	store        Store
	prepared     *PreparedRun
	downstream   engine.Observer
	started      map[string]time.Time
	terminal     *domain.RunEvent
	lastSequence int64
}

func (observer *persistenceObserver) Observe(ctx context.Context, event engine.Event) error {
	safeEvent, runEvent, err := observer.prepareEvent(event)
	if err != nil {
		return err
	}
	if isRunTerminal(event.Type) {
		if observer.terminal != nil {
			return errors.New("duplicate run terminal event")
		}
		stored := cloneRunEvent(runEvent)
		observer.terminal = &stored
		if event.Sequence > observer.lastSequence {
			observer.lastSequence = event.Sequence
		}
		return nil
	}
	var nodeRun *domain.NodeRun
	if event.NodeID != "" {
		built, err := observer.nodeRunForEvent(safeEvent, runEvent.Input, runEvent.Output)
		if err != nil {
			return err
		}
		nodeRun = &built
	}
	if err := observer.store.PersistRunEvent(ctx, runEvent, nodeRun, observer.budget()); err != nil {
		return err
	}
	if event.Sequence > observer.lastSequence {
		observer.lastSequence = event.Sequence
	}
	if observer.downstream != nil {
		return observer.downstream.Observe(ctx, safeEvent)
	}
	return nil
}

func (observer *persistenceObserver) prepareEvent(event engine.Event) (engine.Event, domain.RunEvent, error) {
	safeEvent := event
	inputReport := redactPreparedValue(observer.prepared, event.Input)
	outputReport := redactPreparedValue(observer.prepared, event.Output)
	safeEvent.Input = inputReport.Value
	safeEvent.Output = outputReport.Value
	safeEvent.Error = redactPublicError(observer.prepared, event.Error)
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
		return engine.Event{}, domain.RunEvent{}, err
	}
	outputJSON, err := marshalEventValue(safeEvent.Output)
	if err != nil {
		return engine.Event{}, domain.RunEvent{}, err
	}
	errorJSON, err := marshalEventValue(safeEvent.Error)
	if err != nil {
		return engine.Event{}, domain.RunEvent{}, err
	}
	activePortsJSON, err := json.Marshal(safeEvent.ActivePorts)
	if err != nil {
		return engine.Event{}, domain.RunEvent{}, fmt.Errorf("encode active ports: %w", err)
	}
	inputPathsJSON, err := json.Marshal(safeEvent.InputRedactedPaths)
	if err != nil {
		return engine.Event{}, domain.RunEvent{}, fmt.Errorf("encode input redaction paths: %w", err)
	}
	outputPathsJSON, err := json.Marshal(safeEvent.OutputRedactedPaths)
	if err != nil {
		return engine.Event{}, domain.RunEvent{}, fmt.Errorf("encode output redaction paths: %w", err)
	}
	if len(inputJSON) > 1<<20 || len(outputJSON) > 1<<20 || len(errorJSON) > 64<<10 {
		return engine.Event{}, domain.RunEvent{}, domain.ErrRunEventBudgetExceeded
	}
	runEvent := domain.RunEvent{
		RunID: safeEvent.RunID, Sequence: safeEvent.Sequence, Type: safeEvent.Type, NodeID: safeEvent.NodeID,
		Status: safeEvent.Status, Input: inputJSON, Output: outputJSON, ActivePorts: append([]string(nil), safeEvent.ActivePorts...),
		Error: safeEvent.Error, InputRedactedPaths: append([]string(nil), safeEvent.InputRedactedPaths...),
		OutputRedactedPaths: append([]string(nil), safeEvent.OutputRedactedPaths...), Timestamp: safeEvent.Timestamp,
		DataBytes: int64(len(inputJSON) + len(outputJSON) + len(errorJSON) + len(activePortsJSON) + len(inputPathsJSON) + len(outputPathsJSON)),
	}
	return safeEvent, runEvent, nil
}

func (observer *persistenceObserver) budget() domain.RunEventBudget {
	return domain.RunEventBudget{MaxEvents: 2*len(observer.prepared.Plan.Nodes) + 2, MaxTotalDataBytes: 16 << 20}
}

func isRunTerminal(eventType string) bool {
	switch eventType {
	case "run.completed", "run.failed", "run.cancelled":
		return true
	default:
		return false
	}
}

func redactPreparedValue(prepared *PreparedRun, value any) RedactionReport {
	secretReport := RedactionReport{Value: value, Paths: []string{}}
	if prepared != nil && prepared.secretRedactor != nil {
		secretReport = prepared.secretRedactor.RedactWithReport(value)
	}
	keyReport := RedactWithReport(secretReport.Value)
	paths := append([]string{}, secretReport.Paths...)
	for _, path := range keyReport.Paths {
		appendUniquePath(&paths, path)
	}
	sort.Strings(paths)
	return RedactionReport{Value: keyReport.Value, Paths: paths}
}

func redactPublicError(prepared *PreparedRun, publicError *domain.PublicError) *domain.PublicError {
	if publicError == nil {
		return nil
	}
	safe := *publicError
	if value, ok := redactPreparedValue(prepared, publicError.Code).Value.(string); ok {
		safe.Code = value
	}
	if value, ok := redactPreparedValue(prepared, publicError.Message).Value.(string); ok {
		safe.Message = value
	}
	if value, ok := redactPreparedValue(prepared, publicError.NodeID).Value.(string); ok {
		safe.NodeID = value
	}
	return &safe
}

func cloneRunEvent(event domain.RunEvent) domain.RunEvent {
	cloned := event
	cloned.Input = append(json.RawMessage(nil), event.Input...)
	cloned.Output = append(json.RawMessage(nil), event.Output...)
	cloned.ActivePorts = append([]string{}, event.ActivePorts...)
	cloned.InputRedactedPaths = append([]string{}, event.InputRedactedPaths...)
	cloned.OutputRedactedPaths = append([]string{}, event.OutputRedactedPaths...)
	if event.Error != nil {
		errorCopy := *event.Error
		cloned.Error = &errorCopy
	}
	return cloned
}

func runEventToEngineEvent(event domain.RunEvent) engine.Event {
	converted := engine.Event{
		Sequence: event.Sequence, Type: event.Type, RunID: event.RunID, NodeID: event.NodeID, Status: event.Status,
		ActivePorts: append([]string{}, event.ActivePorts...), Error: redactPublicError(nil, event.Error),
		InputRedactedPaths:  append([]string{}, event.InputRedactedPaths...),
		OutputRedactedPaths: append([]string{}, event.OutputRedactedPaths...), Timestamp: event.Timestamp,
	}
	if len(event.Input) > 0 {
		var input any
		if decodeJSON(event.Input, &input) == nil {
			converted.Input = input
		}
	}
	if len(event.Output) > 0 {
		var output any
		if decodeJSON(event.Output, &output) == nil {
			converted.Output = output
		}
	}
	return converted
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
