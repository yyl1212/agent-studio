package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var errFixtureBranch = errors.New("fixture branch failed")

type memoryObserver struct {
	mu     sync.Mutex
	events []Event
	errAt  string
	err    error
}

type cancellationBlockingObserver struct {
	release chan struct{}
}

func (observer cancellationBlockingObserver) Observe(_ context.Context, event Event) error {
	if event.Type == "run.cancelled" {
		<-observer.release
	}
	return nil
}

func (observer *memoryObserver) Observe(_ context.Context, event Event) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if event.Type == observer.errAt {
		return observer.err
	}
	observer.events = append(observer.events, event)
	return nil
}

func (observer *memoryObserver) Events() []Event {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]Event(nil), observer.events...)
}

type concurrencyTracker struct {
	mu      sync.Mutex
	current int
	maximum int
	started chan struct{}
	release chan struct{}
}

func newConcurrencyTracker() *concurrencyTracker {
	return &concurrencyTracker{started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (tracker *concurrencyTracker) enter(ctx context.Context) error {
	tracker.mu.Lock()
	tracker.current++
	if tracker.current > tracker.maximum {
		tracker.maximum = tracker.current
	}
	tracker.mu.Unlock()
	tracker.started <- struct{}{}
	select {
	case <-tracker.release:
	case <-ctx.Done():
		tracker.leave()
		return ctx.Err()
	}
	tracker.leave()
	return nil
}

func (tracker *concurrencyTracker) leave() {
	tracker.mu.Lock()
	tracker.current--
	tracker.mu.Unlock()
}

func (tracker *concurrencyTracker) Max() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.maximum
}

type runtimeBranchNode struct {
	tracker *concurrencyTracker
	mu      sync.Mutex
	done    map[string]bool
	failure error
}

type runtimeBranchConfig struct {
	Result string `json:"result"`
	Fail   bool   `json:"fail,omitempty"`
	Drop   bool   `json:"drop,omitempty"`
}

func (node *runtimeBranchNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:         "runtime-branch",
		Version:      "1",
		Title:        "Runtime Branch",
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{"result":{"type":"string"},"fail":{"type":"boolean"},"drop":{"type":"boolean"}},"required":["result"],"additionalProperties":false}`),
		Inputs:       []domain.PortDefinition{{Key: "in", Type: domain.TypeString, Required: true, Cardinality: domain.CardinalityOne}},
		Outputs:      []domain.PortDefinition{{Key: "out", Type: domain.TypeString, Cardinality: domain.CardinalityOne}},
	}
}

func (node *runtimeBranchNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	definition := node.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *runtimeBranchNode) Execute(ctx context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	var config runtimeBranchConfig
	if err := json.Unmarshal(request.Config, &config); err != nil {
		return domain.NodeResult{}, err
	}
	if node.tracker != nil {
		if err := node.tracker.enter(ctx); err != nil {
			return domain.NodeResult{}, err
		}
	}
	if config.Fail {
		if node.failure != nil {
			return domain.NodeResult{}, node.failure
		}
		return domain.NodeResult{}, errFixtureBranch
	}
	if config.Drop {
		return domain.NodeResult{Outputs: map[string]any{}}, nil
	}
	node.mu.Lock()
	if node.done == nil {
		node.done = make(map[string]bool)
	}
	node.done[config.Result] = true
	node.mu.Unlock()
	return domain.NodeResult{Outputs: map[string]any{"out": config.Result}}, nil
}

func (node *runtimeBranchNode) completed(result string) bool {
	node.mu.Lock()
	defer node.mu.Unlock()
	return node.done[result]
}

type runtimeJoinNode struct{}

func (*runtimeJoinNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:         "runtime-join",
		Version:      "1",
		Title:        "Runtime Join",
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Inputs: []domain.PortDefinition{
			{Key: "left", Type: domain.TypeString, Required: true, Cardinality: domain.CardinalityOne},
			{Key: "right", Type: domain.TypeString, Required: true, Cardinality: domain.CardinalityOne},
		},
		Outputs: []domain.PortDefinition{{Key: "out", Type: domain.TypeString, Cardinality: domain.CardinalityOne}},
	}
}

func (node *runtimeJoinNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	definition := node.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (*runtimeJoinNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	joined := fmt.Sprintf("%s+%s", request.Inputs["left"][0], request.Inputs["right"][0])
	return domain.NodeResult{Outputs: map[string]any{"out": joined}}, nil
}

func TestEngineSkipsInactiveBranchWithoutDeadlock(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	observer := &memoryObserver{}
	result, err := New(Options{MaxParallel: 4}).Run(context.Background(), "run-1", plan, map[string]any{"value": "yes"}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "true-result" {
		t.Fatalf("output=%v", result.Output)
	}
	assertNodeStatus(t, observer.Events(), "false-node", domain.NodeSkipped)
	assertStrictSequences(t, observer.Events())
	assertStartEventInput(t, observer.Events(), "yes")
}

func TestEngineFailsWhenEndReceivesMultipleActiveValues(t *testing.T) {
	plan, _ := compileParallelRuntimeFixture(t, nil, false)
	_, err := New(Options{MaxParallel: 4}).Run(context.Background(), "run-1", plan, map[string]any{"value": "yes"}, &memoryObserver{})
	if !errors.Is(err, builtin.ErrEndMultipleResults) {
		t.Fatalf("error=%v", err)
	}
}

func TestEngineFailsWhenEndReceivesNoActiveValue(t *testing.T) {
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			runtimeNode("start", "start", `{"fields":[{"key":"value","label":"值","type":"text","required":true}]}`),
			runtimeNode("drop", "runtime-branch", `{"result":"unused","drop":true}`),
			runtimeNode("end", "end", `{}`),
		},
		Edges: []domain.Edge{
			edgeFixture("e1", "start", "value", "drop", "in"),
			edgeFixture("e2", "drop", "out", "end", "result"),
		},
	}
	plan, _ := compileRuntimeGraph(t, graph, nil)
	_, err := New(Options{}).Run(context.Background(), "run-1", plan, map[string]any{"value": "x"}, &memoryObserver{})
	if !errors.Is(err, builtin.ErrEndResultMissing) {
		t.Fatalf("error=%v", err)
	}
}

func TestEngineRunsIndependentNodesConcurrently(t *testing.T) {
	tracker := newConcurrencyTracker()
	plan, _ := compileJoinedRuntimeFixture(t, tracker, false)
	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := New(Options{MaxParallel: 2}).Run(context.Background(), "run-1", plan, map[string]any{"value": "x"}, &memoryObserver{})
		done <- outcome{result: result, err: err}
	}()
	for range 2 {
		select {
		case <-tracker.started:
		case <-time.After(time.Second):
			t.Fatal("parallel nodes did not start")
		}
	}
	close(tracker.release)
	outcomeValue := <-done
	if outcomeValue.err != nil {
		t.Fatal(outcomeValue.err)
	}
	if tracker.Max() != 2 || outcomeValue.result.Output != "left+right" {
		t.Fatalf("max=%d result=%+v", tracker.Max(), outcomeValue.result)
	}
}

func TestEngineLetsIndependentBranchFinishAfterSiblingFailure(t *testing.T) {
	tracker := newConcurrencyTracker()
	plan, branch := compileJoinedRuntimeFixture(t, tracker, true)
	done := make(chan error, 1)
	go func() {
		_, err := New(Options{MaxParallel: 2}).Run(context.Background(), "run-1", plan, map[string]any{"value": "x"}, &memoryObserver{})
		done <- err
	}()
	for range 2 {
		select {
		case <-tracker.started:
		case <-time.After(time.Second):
			t.Fatal("sibling branches did not start")
		}
	}
	close(tracker.release)
	if err := <-done; !errors.Is(err, errFixtureBranch) {
		t.Fatalf("error=%v", err)
	}
	if !branch.completed("right") {
		t.Fatal("independent right branch did not complete")
	}
}

func TestEngineWrapsNodeFailureAndPublishesStableError(t *testing.T) {
	plan, branch := compileJoinedRuntimeFixture(t, nil, true)
	branch.failure = agentnode.NewError(
		agentnode.ErrorKindInput,
		"missing_input",
		errors.New("top-secret cause"),
		map[string]any{"Authorization": "Bearer top-secret"},
	)
	observer := &memoryObserver{}
	_, err := New(Options{}).Run(context.Background(), "run-secret", plan, map[string]any{"value": "x"}, observer)
	var executionErr *NodeExecutionError
	if !errors.As(err, &executionErr) {
		t.Fatalf("error %v is not NodeExecutionError", err)
	}
	if executionErr.NodeID != "left" || executionErr.NodeType != "runtime-branch" {
		t.Fatalf("execution error = %+v", executionErr)
	}
	if !errors.Is(err, branch.failure) {
		t.Fatal("NodeExecutionError must unwrap the node failure")
	}

	for _, event := range observer.Events() {
		if event.Type != "node.failed" || event.NodeID != "left" {
			continue
		}
		if event.Error == nil || event.Error.Code != "NODE_EXECUTION_FAILED" ||
			event.Error.Kind != agentnode.ErrorKindInput || event.Error.Message != "节点输入无效" {
			t.Fatalf("public error = %+v", event.Error)
		}
		encoded, marshalErr := json.Marshal(event.Error)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "top-secret") || strings.Contains(string(encoded), "missing_input") {
			t.Fatalf("public event leaked private error data: %s", encoded)
		}
		return
	}
	t.Fatal("node.failed event not found")
}

func TestEngineBindsPublicModelErrorsToCompiledNodeIdentity(t *testing.T) {
	tests := []struct {
		name        string
		nodeType    string
		version     string
		wantCode    string
		wantMessage string
	}{
		{name: "llm v2", nodeType: "llm", version: "2", wantCode: "MODEL_OUTPUT_INVALID", wantMessage: "模型返回结果不符合输出结构"},
		{name: "spoofed by another node", nodeType: "example.model", version: "1", wantCode: "NODE_EXECUTION_FAILED", wantMessage: "节点执行失败"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, branch := compileJoinedRuntimeFixture(t, nil, true)
			branch.failure = agentnode.NewError(agentnode.ErrorKindInternal, "model_output_invalid", errors.New("private model output"), nil)
			compiled := plan.Nodes["left"]
			compiled.Node.Type = test.nodeType
			compiled.Node.TypeVersion = test.version
			plan.Nodes["left"] = compiled
			observer := &memoryObserver{}
			_, _ = New(Options{}).Run(context.Background(), "run-model-error", plan, map[string]any{"value": "x"}, observer)

			for _, event := range observer.Events() {
				if event.Type == "node.failed" && event.NodeID == "left" {
					if event.Error == nil || event.Error.Code != test.wantCode || event.Error.Message != test.wantMessage {
						t.Fatalf("public error=%+v", event.Error)
					}
					return
				}
			}
			t.Fatal("node.failed event not found")
		})
	}
}

func TestEngineStopsOnObserverErrorAndTimeout(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	observerError := errors.New("observer failed")
	_, err := New(Options{}).Run(context.Background(), "run-1", plan, map[string]any{"value": "yes"}, &memoryObserver{errAt: "node.started", err: observerError})
	if !errors.Is(err, observerError) {
		t.Fatalf("observer error=%v", err)
	}

	tracker := newConcurrencyTracker()
	blockingPlan, _ := compileJoinedRuntimeFixture(t, tracker, false)
	_, err = New(Options{MaxParallel: 2, Timeout: 10 * time.Millisecond}).Run(context.Background(), "run-2", blockingPlan, map[string]any{"value": "x"}, &memoryObserver{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
}

func TestEngineStopsWhenCallerCancels(t *testing.T) {
	tracker := newConcurrencyTracker()
	plan, _ := compileJoinedRuntimeFixture(t, tracker, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := New(Options{MaxParallel: 2}).Run(ctx, "run-cancel", plan, map[string]any{"value": "x"}, &memoryObserver{})
		done <- err
	}()
	for range 2 {
		select {
		case <-tracker.started:
		case <-time.After(time.Second):
			t.Fatal("parallel nodes did not start before cancellation")
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestEngineBoundsCancelledEventObservation(t *testing.T) {
	tracker := newConcurrencyTracker()
	plan, _ := compileJoinedRuntimeFixture(t, tracker, false)
	ctx, cancel := context.WithCancel(context.Background())
	observerRelease := make(chan struct{})
	defer close(observerRelease)
	done := make(chan error, 1)
	go func() {
		_, err := New(Options{MaxParallel: 2}).Run(ctx, "run-cancel-observer", plan, map[string]any{"value": "x"}, cancellationBlockingObserver{release: observerRelease})
		done <- err
	}()
	for range 2 {
		select {
		case <-tracker.started:
		case <-time.After(time.Second):
			t.Fatal("parallel nodes did not start before cancellation")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("engine remained blocked while emitting run.cancelled")
	}
}

func compileConditionalRuntimeFixture(t *testing.T, tracker *concurrencyTracker) (*Plan, *runtimeBranchNode) {
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			runtimeNode("start", "start", `{"fields":[{"key":"value","label":"值","type":"text","required":true}]}`),
			runtimeNode("condition", "condition", `{"operator":"equals","compareValue":"yes"}`),
			runtimeNode("true-node", "runtime-branch", `{"result":"true-result"}`),
			runtimeNode("false-node", "runtime-branch", `{"result":"false-result"}`),
			runtimeNode("end", "end", `{}`),
		},
		Edges: []domain.Edge{
			edgeFixture("e1", "start", "value", "condition", "value"),
			edgeFixture("e2", "condition", "true", "true-node", "in"),
			edgeFixture("e3", "condition", "false", "false-node", "in"),
			edgeFixture("e4", "true-node", "out", "end", "result"),
			edgeFixture("e5", "false-node", "out", "end", "result"),
		},
	}
	return compileRuntimeGraph(t, graph, tracker)
}

func compileParallelRuntimeFixture(t *testing.T, tracker *concurrencyTracker, failLeft bool) (*Plan, *runtimeBranchNode) {
	graph := parallelGraphFixture(failLeft, false)
	return compileRuntimeGraph(t, graph, tracker)
}

func compileJoinedRuntimeFixture(t *testing.T, tracker *concurrencyTracker, failLeft bool) (*Plan, *runtimeBranchNode) {
	graph := parallelGraphFixture(failLeft, true)
	return compileRuntimeGraph(t, graph, tracker)
}

func parallelGraphFixture(failLeft, withJoin bool) domain.Graph {
	leftConfig := `{"result":"left"}`
	if failLeft {
		leftConfig = `{"result":"left","fail":true}`
	}
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			runtimeNode("start", "start", `{"fields":[{"key":"value","label":"值","type":"text","required":true}]}`),
			runtimeNode("left", "runtime-branch", leftConfig),
			runtimeNode("right", "runtime-branch", `{"result":"right"}`),
			runtimeNode("end", "end", `{}`),
		},
		Edges: []domain.Edge{
			edgeFixture("e1", "start", "value", "left", "in"),
			edgeFixture("e2", "start", "value", "right", "in"),
		},
	}
	if withJoin {
		graph.Nodes = append(graph.Nodes, runtimeNode("join", "runtime-join", `{}`))
		graph.Edges = append(graph.Edges,
			edgeFixture("e3", "left", "out", "join", "left"),
			edgeFixture("e4", "right", "out", "join", "right"),
			edgeFixture("e5", "join", "out", "end", "result"),
		)
	} else {
		graph.Edges = append(graph.Edges,
			edgeFixture("e3", "left", "out", "end", "result"),
			edgeFixture("e4", "right", "out", "end", "result"),
		)
	}
	return graph
}

func compileRuntimeGraph(t *testing.T, graph domain.Graph, tracker *concurrencyTracker) (*Plan, *runtimeBranchNode) {
	t.Helper()
	registry := nodes.NewRegistry()
	if err := builtin.RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	branch := &runtimeBranchNode{tracker: tracker}
	if err := registry.Register(branch); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&runtimeJoinNode{}); err != nil {
		t.Fatal(err)
	}
	plan, issues := NewCompiler(registry).Compile(graph)
	if len(issues) != 0 {
		t.Fatalf("compile issues=%+v", issues)
	}
	return plan, branch
}

func runtimeNode(id, nodeType, config string) domain.Node {
	return domain.Node{ID: id, Type: nodeType, TypeVersion: "1", Config: json.RawMessage(config)}
}

func assertNodeStatus(t *testing.T, events []Event, nodeID string, status domain.NodeStatus) {
	t.Helper()
	for _, event := range events {
		if event.NodeID == nodeID && event.Status == status {
			return
		}
	}
	t.Fatalf("status %s for node %s not found in %+v", status, nodeID, events)
}

func assertStrictSequences(t *testing.T, events []Event) {
	t.Helper()
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event %d sequence=%d", index, event.Sequence)
		}
	}
}

func assertStartEventInput(t *testing.T, events []Event, want string) {
	t.Helper()
	for _, event := range events {
		if event.Type != "node.started" || event.NodeID != "start" {
			continue
		}
		input, ok := event.Input.(map[string]any)
		if !ok || input["value"] != want {
			t.Fatalf("start input=%v, want value=%q", event.Input, want)
		}
		return
	}
	t.Fatal("start node_started event not found")
}
