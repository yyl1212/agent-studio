package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestDebugOverviewLoadsTestPublishedAndDebugGraphs(t *testing.T) {
	store := newFakeStore(t)
	graph := graphReturning(t, "history")
	version := store.AddVersion(graph, json.RawMessage(`{"type":"object"}`))
	now := time.Now().UTC()
	sourceID, sourceNodeID := "source-run", "template-1"
	store.runs = []domain.Run{
		{ID: sourceID, WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph, StartedAt: now, EndedAt: &now},
		{ID: "test-run", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph, StartedAt: now, EndedAt: &now},
		{ID: "published-run", WorkflowID: store.workflow.ID, WorkflowVersionID: &version.ID, Mode: domain.RunModePublished, Status: domain.RunCompleted, StartedAt: now, EndedAt: &now},
		{ID: "debug-run", WorkflowID: store.workflow.ID, Mode: domain.RunModeDebug, Status: domain.RunCompleted, GraphSnapshot: graph, SourceRunID: &sourceID, SourceNodeID: &sourceNodeID, StartedAt: now, EndedAt: &now},
	}
	for _, runID := range []string{sourceID, "test-run", "published-run", "debug-run"} {
		store.runEvents = append(store.runEvents, completeHistory(runID, domain.RunCompleted, now)...)
	}
	service := NewDebugService(store, newRealCompiler(t))
	for _, runID := range []string{"test-run", "published-run", "debug-run"} {
		t.Run(runID, func(t *testing.T) {
			overview, err := service.Overview(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			if !overview.ReplayAvailable || !overview.RerunAvailable || len(overview.Graph.Nodes) == 0 {
				t.Fatalf("overview=%+v", overview)
			}
			if runID == "debug-run" && (len(overview.SourceChain) != 1 || overview.SourceChain[0].RunID != sourceID || overview.SourceChain[0].SourceNodeID != sourceNodeID) {
				t.Fatalf("source chain=%+v", overview.SourceChain)
			}
		})
	}
}

func TestDebugOverviewDowngradesLegacyAndIncompleteHistory(t *testing.T) {
	graph := graphReturning(t, "history")
	now := time.Now().UTC()
	tests := []struct {
		name   string
		status domain.RunStatus
		events []domain.RunEvent
	}{
		{name: "legacy", status: domain.RunCompleted},
		{name: "running", status: domain.RunRunning, events: completeHistory("run", domain.RunCompleted, now)},
		{name: "sequence gap", status: domain.RunCompleted, events: []domain.RunEvent{{RunID: "run", Sequence: 1, Type: "run.started"}, {RunID: "run", Sequence: 3, Type: "run.completed"}}},
		{name: "terminal mismatch", status: domain.RunFailed, events: completeHistory("run", domain.RunCompleted, now)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore(t)
			store.runs = []domain.Run{{ID: "run", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: test.status, GraphSnapshot: graph, StartedAt: now}}
			store.runEvents = test.events
			store.nodeRuns["start"] = domain.NodeRun{RunID: "run", NodeID: "start", Status: domain.NodeCompleted}
			overview, err := NewDebugService(store, newRealCompiler(t)).Overview(context.Background(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if overview.ReplayAvailable || overview.RerunAvailable || overview.UnavailableReason != "当前运行缺少完整事件" || len(overview.NodeRuns) == 0 {
				t.Fatalf("overview=%+v", overview)
			}
		})
	}
}

func TestDebugEventsUsesExclusiveCursorAndRejectsLegacy(t *testing.T) {
	store := newFakeStore(t)
	now := time.Now().UTC()
	graph := graphReturning(t, "history")
	store.runs = []domain.Run{
		{ID: "complete", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph},
		{ID: "legacy", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted, GraphSnapshot: graph},
	}
	store.runEvents = completeHistory("complete", domain.RunCompleted, now)
	service := NewDebugService(store, newRealCompiler(t))
	page, err := service.Events(context.Background(), "complete", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Sequence != 2 || page.NextAfterSequence != 2 {
		t.Fatalf("page=%+v", page)
	}
	if _, err := service.Events(context.Background(), "legacy", 0); !errors.Is(err, ErrRunReplayUnavailable) {
		t.Fatalf("legacy error=%v", err)
	}
}

func TestDebugOverviewRejectsUnsupportedSnapshot(t *testing.T) {
	store := newFakeStore(t)
	now := time.Now().UTC()
	store.runs = []domain.Run{{
		ID: "unsupported", WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunCompleted,
		GraphSnapshot: json.RawMessage(`{"schemaVersion":1,"nodes":[{"id":"missing","type":"missing","typeVersion":"1","config":{}}],"edges":[]}`),
	}}
	store.runEvents = completeHistory("unsupported", domain.RunCompleted, now)
	_, err := NewDebugService(store, newRealCompiler(t)).Overview(context.Background(), "unsupported")
	if !errors.Is(err, ErrRunSnapshotUnsupported) {
		t.Fatalf("error=%v", err)
	}
}

func TestDebugHistoryAcceptsQueuedPrefixAndMultipleNodeAttempts(t *testing.T) {
	store := newFakeStore(t)
	now := time.Now().UTC()
	run := domain.Run{ID: "attempt-history", Status: domain.RunCompleted}
	first, second := 1, 2
	store.runEvents = []domain.RunEvent{
		{RunID: run.ID, Sequence: 1, Type: "run.queued", Timestamp: now},
		{RunID: run.ID, Sequence: 2, Type: "run.started", Timestamp: now},
		{RunID: run.ID, Sequence: 3, Type: "node.started", NodeID: "work", NodeAttempt: &first, Status: domain.NodeRunning, Timestamp: now},
		{RunID: run.ID, Sequence: 4, Type: "node.failed", NodeID: "work", NodeAttempt: &first, Status: domain.NodeFailed, Timestamp: now},
		{RunID: run.ID, Sequence: 5, Type: "node.started", NodeID: "work", NodeAttempt: &second, Status: domain.NodeRunning, Timestamp: now},
		{RunID: run.ID, Sequence: 6, Type: "node.completed", NodeID: "work", NodeAttempt: &second, Status: domain.NodeCompleted, Timestamp: now},
		{RunID: run.ID, Sequence: 7, Type: "run.completed", Timestamp: now},
	}
	events, err := NewDebugService(store, nil).loadCompleteHistory(context.Background(), run)
	if err != nil || len(events) != 7 {
		t.Fatalf("events=%v error=%v", events, err)
	}
}

func TestDebugHistoryRejectsDuplicateRunStartAndAttemptTerminal(t *testing.T) {
	now := time.Now().UTC()
	attempt := 1
	for _, test := range []struct {
		name   string
		events []domain.RunEvent
	}{
		{name: "duplicate run start", events: []domain.RunEvent{{Type: "run.queued"}, {Type: "run.started"}, {Type: "run.started"}, {Type: "run.completed"}}},
		{name: "duplicate attempt terminal", events: []domain.RunEvent{{Type: "run.started"}, {Type: "node.started", NodeID: "work", NodeAttempt: &attempt}, {Type: "node.completed", NodeID: "work", NodeAttempt: &attempt}, {Type: "node.failed", NodeID: "work", NodeAttempt: &attempt}, {Type: "run.completed"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore(t)
			for index := range test.events {
				test.events[index].RunID = "invalid-history"
				test.events[index].Sequence = int64(index + 1)
				test.events[index].Timestamp = now
			}
			run := domain.Run{ID: "invalid-history", Status: domain.RunCompleted}
			if _, err := NewDebugService(store, nil).validateCompleteHistory(run, test.events); !errors.Is(err, errIncompleteRunHistory) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDebugOverviewProjectionKeepsLatestNodeAttempt(t *testing.T) {
	projected := latestNodeRunAttempts([]domain.NodeRun{
		{NodeID: "work", Attempt: 1, Status: domain.NodeFailed},
		{NodeID: "other", Attempt: 1, Status: domain.NodeCompleted},
		{NodeID: "work", Attempt: 2, Status: domain.NodeCompleted},
	})
	if len(projected) != 2 || projected[0].NodeID != "other" || projected[1].NodeID != "work" || projected[1].Attempt != 2 {
		t.Fatalf("projected=%+v", projected)
	}
}

func TestDebugSourceChainAllows32AndRejects33OrCycle(t *testing.T) {
	for _, test := range []struct {
		name    string
		length  int
		cycle   bool
		wantErr bool
	}{{name: "32 levels", length: 32}, {name: "33 levels", length: 33, wantErr: true}, {name: "cycle", length: 2, cycle: true, wantErr: true}} {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeStore(t)
			graph := graphReturning(t, "history")
			now := time.Now().UTC()
			runs := make([]domain.Run, test.length+1)
			for index := range runs {
				runs[index] = domain.Run{ID: fmt.Sprintf("run-%d", index), WorkflowID: store.workflow.ID, Mode: domain.RunModeDebug, Status: domain.RunCompleted, GraphSnapshot: graph}
				if index < test.length {
					next, node := fmt.Sprintf("run-%d", index+1), "node"
					runs[index].SourceRunID, runs[index].SourceNodeID = &next, &node
				}
			}
			if test.cycle {
				next, node := "run-0", "node"
				runs[len(runs)-1].SourceRunID, runs[len(runs)-1].SourceNodeID = &next, &node
			}
			store.runs = runs
			store.runEvents = completeHistory("run-0", domain.RunCompleted, now)
			overview, err := NewDebugService(store, newRealCompiler(t)).Overview(context.Background(), "run-0")
			if test.wantErr {
				if !errors.Is(err, ErrRunSnapshotUnsupported) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || len(overview.SourceChain) != 32 {
				t.Fatalf("chain=%d error=%v", len(overview.SourceChain), err)
			}
		})
	}
}

func completeHistory(runID string, status domain.RunStatus, now time.Time) []domain.RunEvent {
	terminal := map[domain.RunStatus]string{domain.RunCompleted: "run.completed", domain.RunFailed: "run.failed", domain.RunCancelled: "run.cancelled"}[status]
	return []domain.RunEvent{
		{RunID: runID, Sequence: 1, Type: "run.started", ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
		{RunID: runID, Sequence: 2, Type: terminal, ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now},
	}
}
