package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/store/postgres"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/internal/database"
)

func TestWorkerTakeoverFencesPreviousOwnerAndKeepsEventHistoryLinear(t *testing.T) {
	store := isolatedWorkerStore(t)
	graph := json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`)
	draftRevision := int64(1)
	workflowRecord, err := store.CreateWorkflow(context.Background(), domain.Workflow{
		ID: uuid.NewString(), Name: "接管测试", Slug: "takeover-" + uuid.NewString(),
		DraftRevision: draftRevision, DraftGraph: graph,
		AgentPresentation: workflow.DefaultAgentPresentation("接管测试", "测试"),
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runID := uuid.NewString()
	submission := workflow.RunSubmission{
		Run: domain.Run{
			ID: runID, WorkflowID: workflowRecord.ID, DraftRevision: &draftRevision, GraphSnapshot: graph,
			Mode: domain.RunModeTest, Status: domain.RunQueued, ExecutionProtocol: domain.CurrentExecutionProtocol,
			Input: json.RawMessage(`{}`), InputRedactedPaths: []string{}, StartedAt: now,
		},
		QueuedEvent:  domain.RunEvent{RunID: runID, Sequence: 1, Type: "run.queued", Timestamp: now, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}},
		InputPayload: domain.RunPayload{RunID: runID, Sequence: 0, Kind: domain.RunPayloadInput, ExecutionProtocol: domain.CurrentExecutionProtocol, CipherVersion: 1, Ciphertext: []byte{1}},
	}
	if err := store.SubmitRun(context.Background(), submission); err != nil {
		t.Fatal(err)
	}
	first, claimed, err := store.ClaimRun(context.Background(), "worker-a", 100*time.Millisecond)
	if err != nil || !claimed {
		t.Fatalf("first=%+v claimed=%v error=%v", first, claimed, err)
	}
	attempt := 1
	started := domain.RunEvent{RunID: runID, Sequence: 2, Type: "node.started", NodeID: "node-a", NodeAttempt: &attempt, Status: domain.NodeRunning, Timestamp: time.Now().UTC()}
	nodeRun := domain.NodeRun{ID: uuid.NewString(), RunID: runID, NodeID: "node-a", NodeType: "fixture", Attempt: 1, Status: domain.NodeRunning}
	budget := domain.RunEventBudget{MaxEvents: 16, MaxTotalDataBytes: 1 << 20}
	if err := store.PersistLeasedRunEvent(context.Background(), first.Lease, started, &nodeRun, nil, budget); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	second, claimed, err := store.ClaimRun(context.Background(), "worker-b", time.Minute)
	if err != nil || !claimed || second.Lease.Token <= first.Lease.Token {
		t.Fatalf("second=%+v claimed=%v error=%v", second, claimed, err)
	}
	stale := workflow.RunFinalization{
		RunID: runID, Status: domain.RunCompleted, EndedAt: time.Now().UTC(),
		TerminalEvent: domain.RunEvent{RunID: runID, Sequence: 3, Type: "run.completed", Timestamp: time.Now().UTC()}, Budget: budget,
	}
	if _, err := store.FinalizeLeasedRun(context.Background(), first.Lease, stale, nil); !errors.Is(err, domain.ErrRunLeaseLost) {
		t.Fatalf("stale lease error=%v", err)
	}
	terminal, err := store.FinalizeLeasedRun(context.Background(), second.Lease, stale, nil)
	if err != nil || terminal.Sequence != 3 {
		t.Fatalf("terminal=%+v error=%v", terminal, err)
	}
	events, err := store.ListRunEvents(context.Background(), runID, 0, 10)
	if err != nil || len(events) != 3 || events[0].Sequence != 1 || events[1].Sequence != 2 || events[2].Sequence != 3 || events[2].Type != "run.completed" {
		t.Fatalf("events=%+v error=%v", events, err)
	}
}

func isolatedWorkerStore(t *testing.T) *postgres.Store {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	admin, err := database.OpenPool(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "worker_takeover_" + uuid.NewString()
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-csearch_path="+schema)
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	})
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(fmt.Errorf("migrate worker integration schema: %w", err))
	}
	return store
}
