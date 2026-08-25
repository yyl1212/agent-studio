package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type recordingObserver struct {
	events []engine.Event
	err    error
}

type failingRunEngine struct {
	err error
}

func (runtime failingRunEngine) Run(_ context.Context, runID string, _ *engine.Plan, _ map[string]any, _ engine.Observer) (engine.RunResult, error) {
	return engine.RunResult{RunID: runID, EndedAt: time.Now().UTC()}, runtime.err
}

func (runtime failingRunEngine) RunWithScope(_ context.Context, runID string, _ *engine.Plan, _ map[string]any, _ engine.Observer, _ engine.ExecutionScope) (engine.RunResult, error) {
	return engine.RunResult{RunID: runID, EndedAt: time.Now().UTC()}, runtime.err
}

func (observer *recordingObserver) Observe(_ context.Context, event engine.Event) error {
	if observer.err != nil {
		return observer.err
	}
	observer.events = append(observer.events, event)
	return nil
}

func TestPersistenceObserverCommitsRedactedEventBeforeDownstream(t *testing.T) {
	store := newFakeStore(t)
	downstream := &recordingObserver{}
	plan := &engine.Plan{
		Graph: domain.Graph{Nodes: []domain.Node{{ID: "node"}}},
		Nodes: map[string]engine.CompiledNode{"node": {Node: domain.Node{ID: "node", Type: "fixture"}}},
	}
	observer := &persistenceObserver{
		store: store, prepared: &PreparedRun{RunID: "run-1", Plan: plan}, downstream: downstream,
		started: make(map[string]time.Time),
	}
	event := engine.Event{
		Sequence: 1, RunID: "run-1", Type: "node.started", NodeID: "node", Status: domain.NodeRunning,
		Input: map[string]any{"api/token": "top-secret"}, ActivePorts: []string{}, Timestamp: time.Now().UTC(),
	}
	if err := observer.Observe(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(store.runEvents) != 1 || len(downstream.events) != 1 {
		t.Fatalf("stored=%d downstream=%d", len(store.runEvents), len(downstream.events))
	}
	persisted := store.runEvents[0]
	if string(persisted.Input) != `{"api/token":"[REDACTED]"}` || !reflect.DeepEqual(persisted.InputRedactedPaths, []string{"/api~1token"}) {
		t.Fatalf("persisted=%+v", persisted)
	}
	if store.eventBudget.MaxEvents != 4 || store.eventBudget.MaxTotalDataBytes != 16<<20 || persisted.DataBytes == 0 {
		t.Fatalf("budget=%+v bytes=%d", store.eventBudget, persisted.DataBytes)
	}
	if got := downstream.events[0].Input.(map[string]any)["api/token"]; got != "[REDACTED]" {
		t.Fatalf("downstream secret=%v", got)
	}
	if !reflect.DeepEqual(downstream.events[0].InputRedactedPaths, []string{"/api~1token"}) {
		t.Fatalf("downstream paths=%v", downstream.events[0].InputRedactedPaths)
	}

	store.failPersist = errors.New("persist failed")
	downstream.events = nil
	if err := observer.Observe(context.Background(), event); !errors.Is(err, store.failPersist) {
		t.Fatalf("error=%v", err)
	}
	if len(downstream.events) != 0 {
		t.Fatalf("downstream observed uncommitted event=%+v", downstream.events)
	}
}

func TestRunEventBudgetsRejectOffendingEventWithoutDownstreamDelivery(t *testing.T) {
	tests := []struct {
		name  string
		event engine.Event
	}{
		{name: "input over one MiB", event: engine.Event{Input: strings.Repeat("x", 1<<20)}},
		{name: "output over one MiB", event: engine.Event{Output: strings.Repeat("x", 1<<20)}},
		{name: "error over 64 KiB", event: engine.Event{Error: &domain.PublicError{Code: "TOO_LARGE", Message: strings.Repeat("x", 64<<10)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore(t)
			downstream := &recordingObserver{}
			observer := &persistenceObserver{
				store: store, prepared: &PreparedRun{RunID: "run-budget", Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{}}},
				downstream: downstream, started: make(map[string]time.Time),
			}
			test.event.Sequence = 1
			test.event.RunID = "run-budget"
			test.event.Type = "run.started"
			test.event.Timestamp = time.Now().UTC()
			if err := observer.Observe(context.Background(), test.event); !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
				t.Fatalf("error=%v", err)
			}
			if len(store.runEvents) != 0 || len(downstream.events) != 0 {
				t.Fatalf("stored=%d downstream=%d", len(store.runEvents), len(downstream.events))
			}
		})
	}
}

func TestRunEventCountAndTotalBudgetsKeepPreviouslyCommittedEvents(t *testing.T) {
	t.Run("event count", func(t *testing.T) {
		store := newFakeStore(t)
		downstream := &recordingObserver{}
		observer := &persistenceObserver{
			store: store, prepared: &PreparedRun{RunID: "run-count", Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{"node": {}}}},
			downstream: downstream, started: make(map[string]time.Time),
		}
		for sequence := int64(1); sequence <= 4; sequence++ {
			if err := observer.Observe(context.Background(), engine.Event{Sequence: sequence, RunID: "run-count", Type: "run.started", Timestamp: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		}
		if err := observer.Observe(context.Background(), engine.Event{Sequence: 5, RunID: "run-count", Type: "run.completed", Timestamp: time.Now().UTC()}); !errors.Is(err, domain.ErrRunEventBudgetExceeded) {
			t.Fatalf("error=%v", err)
		}
		if len(store.runEvents) != 4 || len(downstream.events) != 4 {
			t.Fatalf("stored=%d downstream=%d", len(store.runEvents), len(downstream.events))
		}
	})

	t.Run("total bytes", func(t *testing.T) {
		store := newFakeStore(t)
		downstream := &recordingObserver{}
		nodes := make(map[string]engine.CompiledNode, 9)
		for index := range 9 {
			nodes[fmt.Sprintf("node-%d", index)] = engine.CompiledNode{}
		}
		observer := &persistenceObserver{
			store: store, prepared: &PreparedRun{RunID: "run-total", Plan: &engine.Plan{Nodes: nodes}},
			downstream: downstream, started: make(map[string]time.Time),
		}
		payload := strings.Repeat("x", (1<<20)-2)
		var budgetErr error
		for sequence := int64(1); sequence <= 20; sequence++ {
			budgetErr = observer.Observe(context.Background(), engine.Event{
				Sequence: sequence, RunID: "run-total", Type: "run.started", Output: payload, Timestamp: time.Now().UTC(),
			})
			if budgetErr != nil {
				break
			}
		}
		if !errors.Is(budgetErr, domain.ErrRunEventBudgetExceeded) {
			t.Fatalf("error=%v", budgetErr)
		}
		if len(store.runEvents) == 0 || len(store.runEvents) != len(downstream.events) {
			t.Fatalf("stored=%d downstream=%d", len(store.runEvents), len(downstream.events))
		}
	})
}

func TestPrepareAgentUsesRequestedVersionAfterNewPublish(t *testing.T) {
	service, store := newRunServiceFixture(t)
	v1Graph := graphReturning(t, "v1")
	v1Schema, err := inputSchemaForGraph(v1Graph)
	if err != nil {
		t.Fatal(err)
	}
	v1 := store.AddVersion(v1Graph, v1Schema)
	v2Graph := graphReturning(t, "v2")
	v2Schema, err := inputSchemaForGraph(v2Graph)
	if err != nil {
		t.Fatal(err)
	}
	store.SetCurrentVersion(store.AddVersion(v2Graph, v2Schema))

	prepared, err := service.PrepareAgent(context.Background(), "demo", v1.ID, map[string]any{"topic": ""})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), prepared, &recordingObserver{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "v1" {
		t.Fatalf("output=%v", result.Output)
	}
}

func TestArchivedWorkflowRejectsDraftAndAgentRunPreparation(t *testing.T) {
	service, store := newRunServiceFixture(t)
	versionGraph := graphReturning(t, "v1")
	schema, err := inputSchemaForGraph(versionGraph)
	if err != nil {
		t.Fatal(err)
	}
	version := store.AddVersion(versionGraph, schema)
	store.SetCurrentVersion(version)
	archivedAt := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	store.workflow.ArchivedAt = &archivedAt

	if _, err := service.PrepareDraft(context.Background(), store.workflow.ID, store.workflow.DraftRevision, map[string]any{"topic": "Agent"}); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("draft error=%v", err)
	}
	if _, err := service.PrepareAgent(context.Background(), store.workflow.Slug, version.ID, map[string]any{"topic": "Agent"}); !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("agent error=%v", err)
	}
	if len(store.runs) != 0 {
		t.Fatalf("archived preparations created runs=%+v", store.runs)
	}
}

func TestTestRunStoresGraphSnapshotBeforeExecution(t *testing.T) {
	service, store := newRunServiceFixture(t)
	workflow := store.workflow
	prepared, err := service.PrepareDraft(context.Background(), workflow.ID, workflow.DraftRevision, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.LastRun().GraphSnapshot, workflow.DraftGraph) {
		t.Fatal("snapshot not persisted during prepare")
	}
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRejectsUnknownInputAndExecutePropagatesObservers(t *testing.T) {
	service, store := newRunServiceFixture(t)
	versionGraph := graphReturning(t, "v1")
	schema, err := inputSchemaForGraph(versionGraph)
	if err != nil {
		t.Fatal(err)
	}
	version := store.AddVersion(versionGraph, schema)
	store.SetCurrentVersion(version)
	if _, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"unknown": true}); !errors.Is(err, ErrInputValidation) {
		t.Fatalf("input error=%v", err)
	}

	prepared, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	observerError := errors.New("stream failed")
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{err: observerError}); !errors.Is(err, observerError) {
		t.Fatalf("observer error=%v", err)
	}

	prepared, err = service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	store.failPersist = domain.ErrRunEventBudgetExceeded
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{}); !errors.Is(err, store.failPersist) {
		t.Fatalf("persistence error=%v", err)
	}
	failed := store.LastRun()
	if failed.Status != domain.RunFailed || failed.Error == nil || failed.Error.Code != "RUN_EVENT_BUDGET_EXCEEDED" {
		t.Fatalf("failed run=%+v", failed)
	}
}

func TestPreparePersistsRedactedInputAndCancellationFinishesRun(t *testing.T) {
	service, store := newRunServiceFixture(t)
	graph := graphReturningField(t, "", "token")
	schema, err := inputSchemaForGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	version := store.AddVersion(graph, schema)
	store.SetCurrentVersion(version)
	prepared, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"token": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var persistedInput map[string]any
	if err := json.Unmarshal(store.LastRun().Input, &persistedInput); err != nil {
		t.Fatal(err)
	}
	if persistedInput["token"] != "[REDACTED]" {
		t.Fatalf("persisted input=%v", persistedInput)
	}
	recorder := &recordingObserver{}
	if _, err := service.Execute(context.Background(), prepared, recorder); err != nil {
		t.Fatal(err)
	}
	var downstreamInput map[string]any
	for _, event := range recorder.events {
		if event.Type == "node.started" && event.NodeID == "start" {
			downstreamInput = event.Input.(map[string]any)
		}
	}
	var storedNodeInput map[string]any
	if err := json.Unmarshal(store.nodeRuns["start"].Input, &storedNodeInput); err != nil {
		t.Fatal(err)
	}
	if downstreamInput["token"] != "[REDACTED]" || storedNodeInput["token"] != "[REDACTED]" {
		t.Fatalf("downstream=%v stored=%v", downstreamInput, storedNodeInput)
	}

	prepared, err = service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"token": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Execute(ctx, prepared, &recordingObserver{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if store.LastRun().Status != domain.RunCancelled {
		t.Fatalf("run status=%s", store.LastRun().Status)
	}
}

func TestRunServiceLogsStructuredSafeNodeError(t *testing.T) {
	store := newFakeStore(t)
	const runID = "run-structured-error"
	if err := store.CreateRun(context.Background(), domain.Run{ID: runID, Status: domain.RunRunning}); err != nil {
		t.Fatal(err)
	}
	nodeErr := agentnode.NewError(
		agentnode.ErrorKindInput,
		"missing_input",
		errors.New("top-secret cause"),
		map[string]any{
			"Authorization": "Bearer top-secret",
			"field":         "prompt",
		},
	)
	runErr := &engine.NodeExecutionError{NodeID: "llm-1", NodeType: "llm", Err: nodeErr}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	service := NewRunService(store, newRealCompiler(t), failingRunEngine{err: runErr}, WithLogger(logger))
	_, err := service.Execute(context.Background(), &PreparedRun{RunID: runID, Plan: &engine.Plan{}}, &recordingObserver{})
	if !errors.Is(err, nodeErr) {
		t.Fatalf("execute error = %v", err)
	}

	persisted := store.LastRun().Error
	if persisted == nil || persisted.Code != "NODE_EXECUTION_FAILED" || persisted.Kind != agentnode.ErrorKindInput ||
		persisted.NodeID != "llm-1" || persisted.Message != "节点输入无效" {
		t.Fatalf("persisted public error = %+v", persisted)
	}
	if strings.Contains(logs.String(), "top-secret") || strings.Contains(logs.String(), "Bearer") {
		t.Fatalf("structured log leaked secret: %s", logs.String())
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"run_id": "run-structured-error", "node_id": "llm-1", "node_type": "llm",
		"error_kind": "input", "error_code": "missing_input",
	} {
		if got := record[key]; got != want {
			t.Fatalf("log field %s = %v, want %v; record=%v", key, got, want, record)
		}
	}
	details, ok := record["error_details"].(map[string]any)
	if !ok || details["Authorization"] != "[REDACTED]" || details["field"] != "prompt" {
		t.Fatalf("redacted details = %#v", record["error_details"])
	}
	causes, ok := record["error_causes"].([]any)
	if !ok || len(causes) < 3 {
		t.Fatalf("error causes = %#v", record["error_causes"])
	}
}

func TestRunServiceBindsFinalPublicErrorToPreparedPlanIdentity(t *testing.T) {
	tests := []struct {
		name        string
		nodeType    string
		version     string
		includeNode bool
		wantCode    string
		wantMessage string
	}{
		{name: "llm v2", nodeType: "llm", version: "2", includeNode: true, wantCode: "MODEL_OUTPUT_INVALID", wantMessage: "模型返回结果不符合输出结构"},
		{name: "spoofed by another node", nodeType: "example.model", version: "1", includeNode: true, wantCode: "NODE_EXECUTION_FAILED", wantMessage: "节点执行失败"},
		{name: "node missing from plan", wantCode: "NODE_EXECUTION_FAILED", wantMessage: "节点执行失败"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore(t)
			runID := "run-" + strings.ReplaceAll(test.name, " ", "-")
			if err := store.CreateRun(context.Background(), domain.Run{ID: runID, Status: domain.RunRunning}); err != nil {
				t.Fatal(err)
			}
			nodeErr := agentnode.NewError(agentnode.ErrorKindInternal, "model_output_invalid", errors.New("private model output"), nil)
			runErr := &engine.NodeExecutionError{NodeID: "node", NodeType: "untrusted-error-type", Err: nodeErr}
			plan := &engine.Plan{Nodes: map[string]engine.CompiledNode{}}
			if test.includeNode {
				plan.Nodes["node"] = engine.CompiledNode{Node: domain.Node{ID: "node", Type: test.nodeType, TypeVersion: test.version}}
			}
			service := NewRunService(store, newRealCompiler(t), failingRunEngine{err: runErr})
			_, err := service.Execute(context.Background(), &PreparedRun{RunID: runID, Plan: plan}, &recordingObserver{})
			if !errors.Is(err, nodeErr) {
				t.Fatalf("execute error=%v", err)
			}
			persisted := store.LastRun().Error
			if persisted == nil || persisted.Code != test.wantCode || persisted.Message != test.wantMessage {
				t.Fatalf("persisted public error=%+v", persisted)
			}
		})
	}
}

func newRunServiceFixture(t *testing.T) (*RunService, *fakeStore) {
	t.Helper()
	store := newFakeStore(t)
	return NewRunService(store, newRealCompiler(t), engine.New(engine.Options{})), store
}

func inputSchemaForGraph(raw json.RawMessage) (json.RawMessage, error) {
	var graph struct {
		Nodes []struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil {
		return nil, err
	}
	for _, node := range graph.Nodes {
		if node.Type == "start" {
			return builtin.DeriveInputSchema(node.Config)
		}
	}
	return nil, errors.New("start missing")
}
