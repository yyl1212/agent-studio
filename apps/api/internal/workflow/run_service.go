package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/engine"
	"github.com/google/uuid"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
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
}

type RunService struct {
	store    Store
	compiler Compiler
	engine   Engine
}

func NewRunService(store Store, compiler Compiler, runtime Engine) *RunService {
	return &RunService{store: store, compiler: compiler, engine: runtime}
}

func (service *RunService) PrepareDraft(ctx context.Context, workflowID string, revision int64, input map[string]any) (*PreparedRun, error) {
	workflow, err := service.store.GetWorkflow(ctx, workflowID)
	if err != nil {
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
	inputJSON, err := json.Marshal(Redact(input))
	if err != nil {
		return nil, fmt.Errorf("encode run input: %w", err)
	}
	run := domain.Run{
		ID:            runID,
		WorkflowID:    workflow.ID,
		DraftRevision: &revision,
		GraphSnapshot: append(json.RawMessage(nil), workflow.DraftGraph...),
		Mode:          domain.RunModeTest,
		Status:        domain.RunRunning,
		Input:         inputJSON,
		StartedAt:     time.Now().UTC(),
	}
	if err := service.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return &PreparedRun{
		RunID:         runID,
		Plan:          plan,
		Input:         input,
		Mode:          domain.RunModeTest,
		WorkflowID:    workflow.ID,
		DraftRevision: &revision,
	}, nil
}

func (service *RunService) PrepareAgent(ctx context.Context, slug, workflowVersionID string, input map[string]any) (*PreparedRun, error) {
	workflow, version, err := service.store.GetAgentVersion(ctx, slug, workflowVersionID)
	if err != nil {
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
	inputJSON, err := json.Marshal(Redact(input))
	if err != nil {
		return nil, fmt.Errorf("encode run input: %w", err)
	}
	versionID := version.ID
	run := domain.Run{
		ID:                runID,
		WorkflowID:        workflow.ID,
		WorkflowVersionID: &versionID,
		Mode:              domain.RunModePublished,
		Status:            domain.RunRunning,
		Input:             inputJSON,
		StartedAt:         time.Now().UTC(),
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
	}, nil
}

func (service *RunService) Execute(ctx context.Context, prepared *PreparedRun, observer engine.Observer) (engine.RunResult, error) {
	persistence := &persistenceObserver{
		store:      service.store,
		prepared:   prepared,
		downstream: observer,
		started:    make(map[string]time.Time),
	}
	result, runErr := service.engine.Run(ctx, prepared.RunID, prepared.Plan, prepared.Input, persistence)
	status := domain.RunCompleted
	var publicError *domain.PublicError
	if runErr != nil {
		status = domain.RunFailed
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			status = domain.RunCancelled
		}
		publicError = &domain.PublicError{Code: "RUN_FAILED", Message: "运行失败"}
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
	safeEvent.Input = Redact(event.Input)
	safeEvent.Output = Redact(event.Output)
	if event.NodeID != "" {
		if err := observer.persistNodeEvent(ctx, safeEvent); err != nil {
			return err
		}
	}
	if observer.downstream != nil {
		return observer.downstream.Observe(ctx, safeEvent)
	}
	return nil
}

func (observer *persistenceObserver) persistNodeEvent(ctx context.Context, event engine.Event) error {
	compiled, exists := observer.prepared.Plan.Nodes[event.NodeID]
	if !exists {
		return fmt.Errorf("event references unknown node %s", event.NodeID)
	}
	inputJSON, err := marshalEventValue(event.Input)
	if err != nil {
		return err
	}
	outputJSON, err := marshalEventValue(event.Output)
	if err != nil {
		return err
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
	return observer.store.UpsertNodeRun(ctx, domain.NodeRun{
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
	})
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
