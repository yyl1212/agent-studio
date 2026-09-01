package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRunJSONOmitsAgentRequestKey(t *testing.T) {
	key := "00000000-0000-4000-8000-000000000901"
	run := Run{ID: "run-1", AgentRequestKey: &key}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), "agentRequestKey") {
		t.Fatalf("run JSON leaked agent request key: %s", encoded)
	}
}

func TestRunStatusClassifications(t *testing.T) {
	tests := []struct {
		name        string
		status      RunStatus
		active      bool
		cancellable bool
		terminal    bool
	}{
		{name: "queued", status: RunQueued, active: true, cancellable: true},
		{name: "running", status: RunRunning, active: true, cancellable: true},
		{name: "recovery required", status: RunRecoveryRequired, active: true, cancellable: true},
		{name: "cancelling", status: RunCancelling, active: true, cancellable: true},
		{name: "completed", status: RunCompleted, terminal: true},
		{name: "failed", status: RunFailed, terminal: true},
		{name: "cancelled", status: RunCancelled, terminal: true},
		{name: "unknown", status: RunStatus("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsActiveRunStatus(tt.status); got != tt.active {
				t.Fatalf("IsActiveRunStatus(%q) = %v; want %v", tt.status, got, tt.active)
			}
			if got := IsCancellableRunStatus(tt.status); got != tt.cancellable {
				t.Fatalf("IsCancellableRunStatus(%q) = %v; want %v", tt.status, got, tt.cancellable)
			}
			if got := IsTerminalRunStatus(tt.status); got != tt.terminal {
				t.Fatalf("IsTerminalRunStatus(%q) = %v; want %v", tt.status, got, tt.terminal)
			}
		})
	}
}

func TestRunJSONOmitsLeaseCoordinationFields(t *testing.T) {
	expiresAt := time.Date(2026, time.September, 1, 2, 3, 4, 0, time.UTC)
	run := Run{
		ID:                "run-1",
		ExecutionProtocol: CurrentExecutionProtocol,
		LeaseOwner:        "worker-private-1",
		LeaseToken:        42,
		LeaseExpiresAt:    &expiresAt,
	}

	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"worker-private-1", "leaseOwner", "leaseToken", "leaseExpiresAt"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("run JSON leaked lease coordination field %q: %s", forbidden, encoded)
		}
	}
}

func TestRunAttemptFieldsUsePublicJSONNames(t *testing.T) {
	attempt := 2
	event, err := json.Marshal(RunEvent{RunID: "run-1", Sequence: 3, NodeID: "node-1", NodeAttempt: &attempt})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(event), `"nodeAttempt":2`) {
		t.Fatalf("run event JSON missing nodeAttempt: %s", event)
	}

	nodeRun, err := json.Marshal(NodeRun{ID: "node-run-1", RunID: "run-1", NodeID: "node-1", Attempt: attempt})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nodeRun), `"attempt":2`) {
		t.Fatalf("node run JSON missing attempt: %s", nodeRun)
	}
}
