package worker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const rehydratorKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestRehydratorQueuedRunBuildsInitialCheckpoint(t *testing.T) {
	fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"start": agentnode.ExecutionSafetyPure})
	result := fixture.rehydrate(t, []domain.RunEvent{fixture.event(1, "run.queued", "", 0)}, nil)
	if result.Recovery.Required || result.TerminalStatus != "" || result.Checkpoint.LastSequence != 1 || result.Checkpoint.RunStarted {
		t.Fatalf("result=%+v", result)
	}
}

func TestRehydratorRestoresCompletedAndSkippedFrozenEdges(t *testing.T) {
	fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{
		"start": agentnode.ExecutionSafetyPure, "done": agentnode.ExecutionSafetyPure, "skipped": agentnode.ExecutionSafetyPure,
	})
	fixture.plan.Graph.Edges = []domain.Edge{
		{ID: "done-edge", Source: "done", SourcePort: "out", Target: "start", TargetPort: "in"},
		{ID: "skip-edge", Source: "skipped", SourcePort: "out", Target: "start", TargetPort: "in"},
	}
	fixture.plan.Outgoing["done"] = []domain.Edge{fixture.plan.Graph.Edges[0]}
	fixture.plan.Outgoing["skipped"] = []domain.Edge{fixture.plan.Graph.Edges[1]}
	events := []domain.RunEvent{
		fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0),
		fixture.event(3, "node.started", "done", 1), fixture.event(4, "node.completed", "done", 1),
		fixture.event(5, "node.skipped", "skipped", 1),
	}
	events[3].ActivePorts = []string{"out"}
	payloads := []domain.RunPayload{
		fixture.payload(t, 3, domain.RunPayloadNodeInput, "done", 1, map[string]any{"in": "x"}),
		fixture.payload(t, 4, domain.RunPayloadNodeOutput, "done", 1, map[string]any{"outputs": map[string]any{"out": "ok"}, "activePorts": []string{"out"}}),
	}
	result := fixture.rehydrate(t, events, payloads)
	if result.Recovery.Required || result.Checkpoint.NodeStatuses["done"] != domain.NodeCompleted || result.Checkpoint.NodeStatuses["skipped"] != domain.NodeSkipped {
		t.Fatalf("result=%+v", result)
	}
	if edge := result.Checkpoint.FrozenEdges["done-edge"]; !edge.Active || edge.Value != "ok" {
		t.Fatalf("done edge=%+v", edge)
	}
	if result.Checkpoint.FrozenEdges["skip-edge"].Active {
		t.Fatalf("skip edge=%+v", result.Checkpoint.FrozenEdges["skip-edge"])
	}
}

func TestRehydratorClassifiesUncertainNodesBySafetyAndAttempt(t *testing.T) {
	for _, test := range []struct {
		name       string
		safety     agentnode.ExecutionSafety
		attempt    int
		wantReason domain.RunRecoveryReason
		wantAuto   bool
		wantRetry  bool
	}{
		{"pure retries", agentnode.ExecutionSafetyPure, 1, "", true, false},
		{"read only pauses", agentnode.ExecutionSafetyReadOnly, 1, domain.RecoveryUncertainReadOnly, false, true},
		{"side effect pauses", agentnode.ExecutionSafetySideEffect, 1, domain.RecoveryUncertainEffect, false, true},
		{"attempt limit", agentnode.ExecutionSafetyPure, 3, domain.RecoveryAttemptLimit, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"work": test.safety})
			events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "work", test.attempt)}
			payloads := []domain.RunPayload{fixture.payload(t, 3, domain.RunPayloadNodeInput, "work", test.attempt, map[string]any{"in": "x"})}
			result := fixture.rehydrate(t, events, payloads)
			if test.wantAuto {
				if result.Recovery.Required || result.Checkpoint.NodeAttempts["work"] != test.attempt {
					t.Fatalf("result=%+v", result)
				}
				return
			}
			if !result.Recovery.Required || result.Recovery.Reason != test.wantReason || len(result.Recovery.Nodes) != 1 || result.Recovery.Nodes[0].RetryAllowed != test.wantRetry {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestRehydratorKeepsAllParallelUncertainNodes(t *testing.T) {
	fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"read": agentnode.ExecutionSafetyReadOnly, "effect": agentnode.ExecutionSafetySideEffect})
	events := []domain.RunEvent{
		fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0),
		fixture.event(3, "node.started", "read", 1), fixture.event(4, "node.started", "effect", 1),
	}
	payloads := []domain.RunPayload{
		fixture.payload(t, 3, domain.RunPayloadNodeInput, "read", 1, map[string]any{}),
		fixture.payload(t, 4, domain.RunPayloadNodeInput, "effect", 1, map[string]any{}),
	}
	result := fixture.rehydrate(t, events, payloads)
	if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryUncertainEffect || len(result.Recovery.Nodes) != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRehydratorHonorsManualRetryConfirmation(t *testing.T) {
	fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"work": agentnode.ExecutionSafetySideEffect})
	events := []domain.RunEvent{
		fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0),
		fixture.event(3, "node.started", "work", 1), fixture.event(4, "node.retry_confirmed", "work", 1),
		fixture.event(5, "run.queued", "", 0),
	}
	payloads := []domain.RunPayload{fixture.payload(t, 3, domain.RunPayloadNodeInput, "work", 1, map[string]any{})}
	result := fixture.rehydrate(t, events, payloads)
	if result.Recovery.Required || result.Checkpoint.NodeAttempts["work"] != 1 || result.Checkpoint.LastSequence != 5 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRehydratorConvergesNodeFailureAndCompletedEnd(t *testing.T) {
	for _, test := range []struct {
		name, terminal string
		want           domain.RunStatus
	}{
		{"node failure", "node.failed", domain.RunFailed},
		{"completed end", "node.completed", domain.RunCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"end": agentnode.ExecutionSafetyPure})
			fixture.plan.EndNodeID = "end"
			events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "end", 1), fixture.event(4, test.terminal, "end", 1)}
			payloads := []domain.RunPayload{fixture.payload(t, 3, domain.RunPayloadNodeInput, "end", 1, map[string]any{})}
			if test.terminal == "node.completed" {
				payloads = append(payloads, fixture.payload(t, 4, domain.RunPayloadNodeOutput, "end", 1, map[string]any{"outputs": map[string]any{"result": "ok"}, "activePorts": []string{"result"}}))
			}
			result := fixture.rehydrate(t, events, payloads)
			if result.Recovery.Required || result.TerminalStatus != test.want {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestRehydratorRejectsHistoryAndPayloadDamage(t *testing.T) {
	fixture := newRehydratorFixture(t, map[string]agentnode.ExecutionSafety{"work": agentnode.ExecutionSafetyPure})
	t.Run("sequence gap", func(t *testing.T) {
		result := fixture.rehydrate(t, []domain.RunEvent{fixture.event(2, "run.queued", "", 0)}, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryHistoryInvalid {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("empty history", func(t *testing.T) {
		result := fixture.rehydrate(t, nil, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryHistoryInvalid {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("missing node payload", func(t *testing.T) {
		result := fixture.rehydrate(t, []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "work", 1)}, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryPayloadUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("payload metadata does not match event", func(t *testing.T) {
		events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "work", 1)}
		payload := fixture.payload(t, 3, domain.RunPayloadNodeInput, "other", 1, map[string]any{})
		result := fixture.rehydrate(t, events, []domain.RunPayload{payload})
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryPayloadUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("duplicate payload", func(t *testing.T) {
		result := fixture.rehydrate(t, []domain.RunEvent{fixture.event(1, "run.queued", "", 0)}, []domain.RunPayload{fixture.inputPayload})
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryPayloadUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("tampered ciphertext", func(t *testing.T) {
		events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "work", 1)}
		payload := fixture.payload(t, 3, domain.RunPayloadNodeInput, "work", 1, map[string]any{})
		payload.Ciphertext[len(payload.Ciphertext)-1] ^= 1
		result := fixture.rehydrate(t, events, []domain.RunPayload{payload})
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryPayloadUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("wrong encryption key", func(t *testing.T) {
		wrongCipher, err := runpayload.New("ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=")
		if err != nil {
			t.Fatal(err)
		}
		result, err := NewRehydrator(nil, nil, wrongCipher).RehydrateLoaded(context.Background(), fixture.run,
			[]domain.RunEvent{fixture.event(1, "run.queued", "", 0)}, []domain.RunPayload{fixture.inputPayload}, fixture.prepared)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryPayloadUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("unknown node", func(t *testing.T) {
		events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "node.started", "missing", 1)}
		result := fixture.rehydrate(t, events, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryNodeUnavailable {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("unknown protocol", func(t *testing.T) {
		changed := fixture
		changed.run.ExecutionProtocol = 99
		result := changed.rehydrate(t, []domain.RunEvent{changed.event(1, "run.queued", "", 0)}, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryHistoryInvalid {
			t.Fatalf("result=%+v", result)
		}
	})
	t.Run("active run with terminal event", func(t *testing.T) {
		events := []domain.RunEvent{fixture.event(1, "run.queued", "", 0), fixture.event(2, "run.started", "", 0), fixture.event(3, "run.completed", "", 0)}
		result := fixture.rehydrate(t, events, nil)
		if !result.Recovery.Required || result.Recovery.Reason != domain.RecoveryHistoryInvalid {
			t.Fatalf("result=%+v", result)
		}
	})
}

type rehydratorFixture struct {
	cipher       *runpayload.Cipher
	run          domain.Run
	plan         *engine.Plan
	prepared     *workflow.PreparedRun
	inputPayload domain.RunPayload
}

func newRehydratorFixture(t *testing.T, safeties map[string]agentnode.ExecutionSafety) rehydratorFixture {
	t.Helper()
	cipher, err := runpayload.New(rehydratorKey)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "00000000-0000-0000-0000-000000000101", Status: domain.RunRunning, ExecutionProtocol: domain.CurrentExecutionProtocol, StartedAt: time.Now().UTC()}
	plan := &engine.Plan{Nodes: map[string]engine.CompiledNode{}, Incoming: map[string][]domain.Edge{}, Outgoing: map[string][]domain.Edge{}}
	for nodeID, safety := range safeties {
		plan.Nodes[nodeID] = engine.CompiledNode{Node: domain.Node{ID: nodeID}, ExecutionSafety: safety}
		plan.TopologicalOrder = append(plan.TopologicalOrder, nodeID)
	}
	prepared := &workflow.PreparedRun{RunID: run.ID, Plan: plan, Input: map[string]any{"secret": "value"}}
	fixture := rehydratorFixture{cipher: cipher, run: run, plan: plan, prepared: prepared}
	fixture.inputPayload = fixture.payload(t, 0, domain.RunPayloadInput, "", 0, prepared.Input)
	return fixture
}

func (fixture rehydratorFixture) event(sequence int64, eventType, nodeID string, attempt int) domain.RunEvent {
	event := domain.RunEvent{RunID: fixture.run.ID, Sequence: sequence, Type: eventType, NodeID: nodeID, Timestamp: time.Now().UTC(), ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}}
	if nodeID != "" {
		event.NodeAttempt = &attempt
		switch eventType {
		case "node.started":
			event.Status = domain.NodeRunning
		case "node.completed":
			event.Status = domain.NodeCompleted
		case "node.failed":
			event.Status = domain.NodeFailed
		case "node.skipped":
			event.Status = domain.NodeSkipped
		case "node.cancelled":
			event.Status = domain.NodeCancelled
		}
	}
	return event
}

func (fixture rehydratorFixture) payload(t *testing.T, sequence int64, kind domain.RunPayloadKind, nodeID string, attempt int, value any) domain.RunPayload {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	metadata := runpayload.Metadata{RunID: fixture.run.ID, Sequence: sequence, Kind: kind, NodeID: nodeID, NodeAttempt: attempt, ExecutionProtocol: domain.CurrentExecutionProtocol}
	ciphertext, err := fixture.cipher.Seal(metadata, body)
	if err != nil {
		t.Fatal(err)
	}
	return domain.RunPayload{RunID: fixture.run.ID, Sequence: sequence, Kind: kind, NodeID: nodeID, NodeAttempt: attempt, ExecutionProtocol: domain.CurrentExecutionProtocol, CipherVersion: 1, Ciphertext: ciphertext, CreatedAt: time.Now().UTC()}
}

func (fixture rehydratorFixture) rehydrate(t *testing.T, events []domain.RunEvent, payloads []domain.RunPayload) PreparedExecution {
	t.Helper()
	allPayloads := append([]domain.RunPayload{fixture.inputPayload}, payloads...)
	result, err := NewRehydrator(nil, nil, fixture.cipher).RehydrateLoaded(context.Background(), fixture.run, events, allPayloads, fixture.prepared)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
