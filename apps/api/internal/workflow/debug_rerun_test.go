package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestPreviewRerunFreezesExternalJoinInput(t *testing.T) {
	service, _, _ := newRerunFixture(t)
	preview, err := service.PreviewRerun(context.Background(), "source-run", "left")
	if err != nil {
		t.Fatal(err)
	}
	if preview.SourceRunID != "source-run" || preview.SourceNodeID != "left" {
		t.Fatalf("source=%s/%s", preview.SourceRunID, preview.SourceNodeID)
	}
	if got := rerunNodeIDs(preview.ActiveNodes); !equalStrings(got, []string{"left", "join", "end"}) {
		t.Fatalf("active nodes=%v", got)
	}
	if len(preview.FrozenEdges) != 1 || preview.FrozenEdges[0].EdgeID != "right-join" || !preview.FrozenEdges[0].Active || preview.FrozenEdges[0].Value != "R-old" {
		t.Fatalf("frozen=%+v", preview.FrozenEdges)
	}
	if got := preview.EntryInput["seed"].([]any); len(got) != 1 || got[0] != "old" {
		t.Fatalf("entry input=%#v", preview.EntryInput)
	}
	if preview.EntryInputRedactedPaths == nil || preview.ActiveNodes == nil || preview.FrozenEdges == nil {
		t.Fatal("preview arrays must not be nil")
	}
}

func TestPreviewRerunRejectsMissingOrRedactedFrozenValue(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeStore)
	}{
		{name: "missing terminal", mutate: func(store *fakeStore) {
			store.runEvents = removeRunEvent(store.runEvents, "right", "node.completed")
		}},
		{name: "active value missing", mutate: func(store *fakeStore) {
			for index := range store.runEvents {
				if store.runEvents[index].NodeID == "right" && store.runEvents[index].Type == "node.completed" {
					store.runEvents[index].Output = json.RawMessage(`{}`)
					store.runEvents[index].ActivePorts = []string{"text"}
				}
			}
		}},
		{name: "redacted value", mutate: func(store *fakeStore) {
			for index := range store.runEvents {
				if store.runEvents[index].NodeID == "right" && store.runEvents[index].Type == "node.completed" {
					store.runEvents[index].OutputRedactedPaths = []string{"/text"}
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, _ := newRerunFixture(t)
			test.mutate(store)
			_, err := service.PreviewRerun(context.Background(), "source-run", "left")
			if !errors.Is(err, ErrRunFrozenEdgeUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPreviewRerunUsesLatestCompletedAttemptForEntryAndFrozenEdge(t *testing.T) {
	service, store, _ := newRerunFixture(t)
	now := time.Now().UTC()
	attempt := 2
	terminal := store.runEvents[len(store.runEvents)-1]
	store.runEvents = append(store.runEvents[:len(store.runEvents)-1],
		domain.RunEvent{Type: "node.started", NodeID: "left", NodeAttempt: &attempt, Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":["new-left"]}`), Timestamp: now},
		domain.RunEvent{Type: "node.completed", NodeID: "left", NodeAttempt: &attempt, Status: domain.NodeCompleted, Output: json.RawMessage(`{"text":"L-new"}`), Timestamp: now},
		domain.RunEvent{Type: "node.started", NodeID: "right", NodeAttempt: &attempt, Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":["new-right"]}`), Timestamp: now},
		domain.RunEvent{Type: "node.completed", NodeID: "right", NodeAttempt: &attempt, Status: domain.NodeCompleted, Output: json.RawMessage(`{"text":"R-new"}`), Timestamp: now},
		terminal,
	)
	for index := range store.runEvents {
		store.runEvents[index].RunID = "source-run"
		store.runEvents[index].Sequence = int64(index + 1)
	}
	entry, err := service.PreviewRerun(context.Background(), "source-run", "left")
	if err != nil || entry.EntryInput["seed"].([]any)[0] != "new-left" || entry.FrozenEdges[0].Value != "R-new" {
		t.Fatalf("preview=%+v error=%v", entry, err)
	}
}

func TestPreviewRerunRejectsFrozenNodeWithLaterUnresolvedAttempt(t *testing.T) {
	service, store, _ := newRerunFixture(t)
	attempt := 2
	terminal := store.runEvents[len(store.runEvents)-1]
	store.runEvents = append(store.runEvents[:len(store.runEvents)-1], domain.RunEvent{
		Type: "node.started", NodeID: "right", NodeAttempt: &attempt, Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":["uncertain"]}`), Timestamp: time.Now().UTC(),
	}, terminal)
	for index := range store.runEvents {
		store.runEvents[index].RunID = "source-run"
		store.runEvents[index].Sequence = int64(index + 1)
	}
	if _, err := service.PreviewRerun(context.Background(), "source-run", "left"); !errors.Is(err, ErrRunFrozenEdgeUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestPrepareRerunUsesEditedEntryInputAndCreatesDebugRun(t *testing.T) {
	service, store, graph := newRerunFixture(t)
	before := len(store.runs)
	prepared, err := service.PrepareRerun(context.Background(), "source-run", "left", RerunRequest{
		EntryInput: map[string]any{"seed": []any{"edited"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.runs) != before+1 {
		t.Fatalf("runs=%d before=%d", len(store.runs), before)
	}
	created := store.LastRun()
	if created.Mode != domain.RunModeDebug || created.SourceRunID == nil || *created.SourceRunID != "source-run" || created.SourceNodeID == nil || *created.SourceNodeID != "left" {
		t.Fatalf("created=%+v", created)
	}
	if prepared.sourceRunID != "source-run" || prepared.sourceNodeID != "left" {
		t.Fatalf("prepared source=%q/%q", prepared.sourceRunID, prepared.sourceNodeID)
	}
	if string(created.GraphSnapshot) != string(graph) || prepared.Scope == nil || prepared.Scope.EntryNodeID != "left" {
		t.Fatalf("prepared=%+v graph=%s", prepared, created.GraphSnapshot)
	}
	if got := prepared.Scope.EntryNodeInputs["seed"]; len(got) != 1 || got[0] != "edited" {
		t.Fatalf("entry inputs=%#v", prepared.Scope.EntryNodeInputs)
	}
	if frozen := prepared.Scope.FrozenEdges["right-join"]; !frozen.Active || frozen.Value != "R-old" {
		t.Fatalf("frozen=%+v", frozen)
	}
}

func TestSubmitRerunQueuesAtomicallyWithoutLegacyCreate(t *testing.T) {
	legacy, store, _ := newRerunFixture(t)
	before := len(store.runs)
	cipher, err := runpayload.New("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	durable := &submissionStore{}
	service := NewQueuedDebugService(store, legacy.compiler, NewRunSubmissionService(durable, cipher))
	result, err := service.SubmitRerun(context.Background(), "source-run", "left", RerunRequest{EntryInput: map[string]any{"seed": []any{"edited"}}})
	if err != nil || result.Status != domain.RunQueued || durable.calls != 1 || len(store.runs) != before {
		t.Fatalf("result=%+v calls=%d runs=%d before=%d error=%v", result, durable.calls, len(store.runs), before, err)
	}
	if durable.submission.Run.SourceRunID == nil || durable.submission.Run.SourceNodeID == nil || durable.submission.Run.Mode != domain.RunModeDebug {
		t.Fatalf("submission=%+v", durable.submission)
	}
}

func TestPrepareRerunPersistsInputRedactedPaths(t *testing.T) {
	service, store, _ := newRerunFixture(t)
	prepared, err := service.PrepareRerun(context.Background(), "source-run", "start", RerunRequest{
		EntryInput: map[string]any{"seed": "公开值", "webhookToken": "do-not-persist"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created := store.LastRun()
	if string(created.Input) != `{"seed":"公开值","webhookToken":"[REDACTED]"}` || !equalStrings(created.InputRedactedPaths, []string{"/webhookToken"}) {
		t.Fatalf("created input=%s paths=%v", created.Input, created.InputRedactedPaths)
	}
	if created.InputRedactedPaths == nil || prepared.secretRedactor == nil {
		t.Fatal("debug prepare must retain non-nil redaction state")
	}
}

func TestPrepareRerunRequiresConfirmationForSideEffectsAndUnknownSafety(t *testing.T) {
	for _, safety := range []agentnode.ExecutionSafety{agentnode.ExecutionSafetySideEffect, "future"} {
		t.Run(string(safety), func(t *testing.T) {
			service, store := newSafetyRerunFixture(t, safety)
			before := len(store.runs)
			preview, err := service.PreviewRerun(context.Background(), "source-run", "action")
			if err != nil {
				t.Fatal(err)
			}
			if preview.EffectiveSafety != agentnode.ExecutionSafetySideEffect || !preview.RequiresConfirmation {
				t.Fatalf("preview=%+v", preview)
			}
			_, err = service.PrepareRerun(context.Background(), "source-run", "action", RerunRequest{EntryInput: map[string]any{"in": []any{"edited"}}})
			if !errors.Is(err, ErrRunSideEffectConfirmationRequired) || len(store.runs) != before {
				t.Fatalf("error=%v runs=%d", err, len(store.runs))
			}
			if _, err := service.PrepareRerun(context.Background(), "source-run", "action", RerunRequest{EntryInput: map[string]any{"in": []any{"edited"}}, ConfirmSideEffects: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPrepareRerunWarnsButDoesNotRequireConfirmationForReadOnly(t *testing.T) {
	service, _ := newSafetyRerunFixture(t, agentnode.ExecutionSafetyReadOnly)
	preview, err := service.PreviewRerun(context.Background(), "source-run", "action")
	if err != nil {
		t.Fatal(err)
	}
	if preview.EffectiveSafety != agentnode.ExecutionSafetyReadOnly || preview.RequiresConfirmation {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := service.PrepareRerun(context.Background(), "source-run", "action", RerunRequest{EntryInput: map[string]any{"in": []any{"edited"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRerunRejectsSkippedEntryAndRedactedPlaceholder(t *testing.T) {
	t.Run("skipped entry", func(t *testing.T) {
		service, store, _ := newRerunFixture(t)
		store.runEvents = removeRunEvent(store.runEvents, "left", "node.started")
		for index := range store.runEvents {
			if store.runEvents[index].NodeID == "left" && store.runEvents[index].Type == "node.completed" {
				store.runEvents[index].Type = "node.skipped"
				store.runEvents[index].Status = domain.NodeSkipped
			}
		}
		before := len(store.runs)
		_, err := service.PrepareRerun(context.Background(), "source-run", "left", RerunRequest{EntryInput: map[string]any{"seed": []any{"edited"}}})
		if !errors.Is(err, ErrRunEntryInputInvalid) || len(store.runs) != before {
			t.Fatalf("error=%v runs=%d", err, len(store.runs))
		}
	})
	t.Run("redacted placeholder", func(t *testing.T) {
		service, store, _ := newRerunFixture(t)
		for index := range store.runEvents {
			if store.runEvents[index].NodeID == "left" && store.runEvents[index].Type == "node.started" {
				store.runEvents[index].Input = json.RawMessage(`{"seed":["[REDACTED]"]}`)
				store.runEvents[index].InputRedactedPaths = []string{"/seed/0"}
			}
		}
		before := len(store.runs)
		_, err := service.PrepareRerun(context.Background(), "source-run", "left", RerunRequest{EntryInput: map[string]any{"seed": []any{"[REDACTED]"}}})
		if !errors.Is(err, ErrRunEntryInputInvalid) || len(store.runs) != before {
			t.Fatalf("error=%v runs=%d", err, len(store.runs))
		}
	})
}

func TestPrepareRerunValidatesStartSchemaBeforeCreate(t *testing.T) {
	service, store, _ := newRerunFixture(t)
	before := len(store.runs)
	_, err := service.PrepareRerun(context.Background(), "source-run", "start", RerunRequest{EntryInput: map[string]any{"seed": 42}})
	if !errors.Is(err, ErrRunEntryInputInvalid) || len(store.runs) != before {
		t.Fatalf("error=%v runs=%d", err, len(store.runs))
	}
	prepared, err := service.PrepareRerun(context.Background(), "source-run", "start", RerunRequest{EntryInput: map[string]any{"seed": "edited"}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Scope == nil || prepared.Scope.EntryRunInput["seed"] != "edited" || len(prepared.Scope.EntryNodeInputs) != 0 {
		t.Fatalf("scope=%+v", prepared.Scope)
	}
}

func newRerunFixture(t *testing.T) (*DebugService, *fakeStore, json.RawMessage) {
	t.Helper()
	graph := rerunGraph(t)
	store := newFakeStore(t)
	now := time.Now().UTC()
	store.runs = []domain.Run{{ID: "source-run", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph, Input: json.RawMessage(`{"seed":"old"}`), StartedAt: now, EndedAt: &now}}
	store.runEvents = rerunHistory(now)
	return NewDebugService(store, newRealCompiler(t)), store, graph
}

func rerunGraph(t *testing.T) json.RawMessage {
	t.Helper()
	start := json.RawMessage(`{"fields":[{"key":"seed","label":"Seed","type":"text","required":true},{"key":"webhookToken","label":"Token","type":"text","required":false}]}`)
	graph := domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{
		{ID: "start", Type: "start", TypeVersion: "1", Config: start},
		{ID: "left", Type: "template", TypeVersion: "1", Config: json.RawMessage(`{"template":"L-{{seed}}"}`)},
		{ID: "right", Type: "template", TypeVersion: "1", Config: json.RawMessage(`{"template":"R-{{seed}}"}`)},
		{ID: "join", Type: "template", TypeVersion: "1", Config: json.RawMessage(`{"template":"{{left}}/{{right}}"}`)},
		{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
	}, Edges: []domain.Edge{
		{ID: "start-left", Source: "start", SourcePort: "seed", Target: "left", TargetPort: "seed"},
		{ID: "start-right", Source: "start", SourcePort: "seed", Target: "right", TargetPort: "seed"},
		{ID: "left-join", Source: "left", SourcePort: "text", Target: "join", TargetPort: "left"},
		{ID: "right-join", Source: "right", SourcePort: "text", Target: "join", TargetPort: "right"},
		{ID: "join-end", Source: "join", SourcePort: "text", Target: "end", TargetPort: "result"},
	}}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func rerunHistory(now time.Time) []domain.RunEvent {
	events := []domain.RunEvent{
		{Type: "run.started"},
		{Type: "node.started", NodeID: "start", Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":"old"}`)},
		{Type: "node.completed", NodeID: "start", Status: domain.NodeCompleted, Output: json.RawMessage(`{"seed":"old"}`)},
		{Type: "node.started", NodeID: "left", Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":["old"]}`)},
		{Type: "node.started", NodeID: "right", Status: domain.NodeRunning, Input: json.RawMessage(`{"seed":["old"]}`)},
		{Type: "node.completed", NodeID: "left", Status: domain.NodeCompleted, Output: json.RawMessage(`{"text":"L-old"}`)},
		{Type: "node.completed", NodeID: "right", Status: domain.NodeCompleted, Output: json.RawMessage(`{"text":"R-old"}`)},
		{Type: "node.started", NodeID: "join", Status: domain.NodeRunning, Input: json.RawMessage(`{"left":["L-old"],"right":["R-old"]}`)},
		{Type: "node.completed", NodeID: "join", Status: domain.NodeCompleted, Output: json.RawMessage(`{"text":"L-old/R-old"}`)},
		{Type: "node.started", NodeID: "end", Status: domain.NodeRunning, Input: json.RawMessage(`{"result":["L-old/R-old"]}`)},
		{Type: "node.completed", NodeID: "end", Status: domain.NodeCompleted, Output: json.RawMessage(`{"result":"L-old/R-old"}`)},
		{Type: "run.completed"},
	}
	for index := range events {
		events[index].RunID = "source-run"
		events[index].Sequence = int64(index + 1)
		events[index].Timestamp = now
		events[index].ActivePorts = []string{}
		events[index].InputRedactedPaths = []string{}
		events[index].OutputRedactedPaths = []string{}
	}
	return events
}

type debugSafetyNode struct{ safety agentnode.ExecutionSafety }

func (node debugSafetyNode) Definition() agentnode.Definition {
	return agentnode.Definition{Type: "debug-safety", Version: "1", Title: "Safety", ConfigSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), ExecutionSafety: node.safety}
}
func (debugSafetyNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	return agentnode.ResolvedPorts{Inputs: []agentnode.Port{{Key: "in", Type: agentnode.DataTypeAny, Required: true, Cardinality: agentnode.CardinalityOne}}, Outputs: []agentnode.Port{{Key: "out", Type: agentnode.DataTypeAny, Cardinality: agentnode.CardinalityOne}}}, nil
}
func (debugSafetyNode) Execute(context.Context, agentnode.Request) (agentnode.Result, error) {
	return agentnode.Result{}, nil
}

func newSafetyRerunFixture(t *testing.T, safety agentnode.ExecutionSafety) (*DebugService, *fakeStore) {
	t.Helper()
	registry := nodes.NewRegistry()
	if err := builtin.RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(debugSafetyNode{safety: safety}); err != nil {
		t.Fatal(err)
	}
	graph, err := json.Marshal(domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{
		{ID: "start", Type: "start", TypeVersion: "1", Config: json.RawMessage(`{"fields":[{"key":"value","label":"Value","type":"text","required":true}]}`)},
		{ID: "action", Type: "debug-safety", TypeVersion: "1", Config: json.RawMessage(`{}`)},
		{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
	}, Edges: []domain.Edge{
		{ID: "to-action", Source: "start", SourcePort: "value", Target: "action", TargetPort: "in"},
		{ID: "to-end", Source: "action", SourcePort: "out", Target: "end", TargetPort: "result"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeStore(t)
	now := time.Now().UTC()
	store.runs = []domain.Run{{ID: "source-run", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph, Input: json.RawMessage(`{"value":"old"}`)}}
	store.runEvents = []domain.RunEvent{
		{RunID: "source-run", Sequence: 1, Type: "run.started", Timestamp: now},
		{RunID: "source-run", Sequence: 2, Type: "node.started", NodeID: "action", Status: domain.NodeRunning, Input: json.RawMessage(`{"in":["old"]}`), Timestamp: now},
		{RunID: "source-run", Sequence: 3, Type: "node.completed", NodeID: "action", Status: domain.NodeCompleted, Output: json.RawMessage(`{"out":"old"}`), Timestamp: now},
		{RunID: "source-run", Sequence: 4, Type: "node.started", NodeID: "end", Status: domain.NodeRunning, Input: json.RawMessage(`{"result":["old"]}`), Timestamp: now},
		{RunID: "source-run", Sequence: 5, Type: "node.completed", NodeID: "end", Status: domain.NodeCompleted, Output: json.RawMessage(`{"result":"old"}`), Timestamp: now},
		{RunID: "source-run", Sequence: 6, Type: "run.completed", Timestamp: now},
	}
	return NewDebugService(store, engine.NewCompiler(registry)), store
}

func removeRunEvent(events []domain.RunEvent, nodeID, eventType string) []domain.RunEvent {
	filtered := make([]domain.RunEvent, 0, len(events))
	for _, event := range events {
		if event.NodeID == nodeID && event.Type == eventType {
			continue
		}
		event.Sequence = int64(len(filtered) + 1)
		filtered = append(filtered, event)
	}
	return filtered
}

func rerunNodeIDs(nodes []RerunNode) []string {
	ids := make([]string, len(nodes))
	for index := range nodes {
		ids[index] = nodes[index].ID
	}
	return ids
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
