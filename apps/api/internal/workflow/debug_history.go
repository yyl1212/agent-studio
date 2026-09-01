package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

var errIncompleteRunHistory = errors.New("incomplete run history")

func NewDebugService(store Store, compiler Compiler) *DebugService {
	return &DebugService{store: store, compiler: compiler}
}

func (service *DebugService) Overview(ctx context.Context, runID string) (DebugOverview, error) {
	run, nodeRuns, err := service.store.GetRun(ctx, runID)
	if err != nil {
		return DebugOverview{}, err
	}
	graph, _, err := service.loadRunGraph(ctx, run)
	if err != nil {
		return DebugOverview{}, err
	}
	sources, err := service.loadSourceChain(ctx, run)
	if err != nil {
		return DebugOverview{}, err
	}
	nodeRuns = latestNodeRunAttempts(nodeRuns)
	overview := DebugOverview{Run: run, Graph: graph, NodeRuns: nodeRuns, SourceChain: sources}
	if _, err := service.loadCompleteHistory(ctx, run); err != nil {
		if errors.Is(err, errIncompleteRunHistory) {
			overview.UnavailableReason = "当前运行缺少完整事件"
			return overview, nil
		}
		return DebugOverview{}, err
	}
	overview.ReplayAvailable = true
	overview.RerunAvailable = true
	return overview, nil
}

func latestNodeRunAttempts(values []domain.NodeRun) []domain.NodeRun {
	latest := make(map[string]domain.NodeRun, len(values))
	for _, value := range values {
		current, exists := latest[value.NodeID]
		if !exists || value.Attempt > current.Attempt {
			latest[value.NodeID] = value
		}
	}
	result := make([]domain.NodeRun, 0, len(latest))
	for _, value := range latest {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].NodeID < result[right].NodeID })
	return result
}

func (service *DebugService) Events(ctx context.Context, runID string, afterSequence int64) (RunEventPage, error) {
	run, _, err := service.store.GetRun(ctx, runID)
	if err != nil {
		return RunEventPage{}, err
	}
	if _, err := service.loadCompleteHistory(ctx, run); err != nil {
		if errors.Is(err, errIncompleteRunHistory) {
			return RunEventPage{}, ErrRunReplayUnavailable
		}
		return RunEventPage{}, err
	}
	events, err := service.store.ListRunEvents(ctx, runID, afterSequence, 200)
	if err != nil {
		return RunEventPage{}, err
	}
	if events == nil {
		events = []domain.RunEvent{}
	}
	next := afterSequence
	if len(events) > 0 {
		next = events[len(events)-1].Sequence
	}
	return RunEventPage{Events: events, NextAfterSequence: next}, nil
}

func (service *DebugService) loadRunGraph(ctx context.Context, run domain.Run) (domain.Graph, *engine.Plan, error) {
	_, graph, plan, err := service.loadRunGraphData(ctx, run)
	return graph, plan, err
}

func (service *DebugService) loadRunGraphData(ctx context.Context, run domain.Run) (json.RawMessage, domain.Graph, *engine.Plan, error) {
	return loadRunGraphData(ctx, service.store, service.compiler, run)
}

type runSnapshotStore interface {
	GetWorkflow(context.Context, string) (domain.Workflow, error)
	GetAgentVersion(context.Context, string, string) (domain.Workflow, domain.WorkflowVersion, error)
}

func loadRunGraphData(ctx context.Context, store runSnapshotStore, compiler Compiler, run domain.Run) (json.RawMessage, domain.Graph, *engine.Plan, error) {
	var raw json.RawMessage
	switch run.Mode {
	case domain.RunModeTest, domain.RunModeDebug:
		raw = run.GraphSnapshot
	case domain.RunModePublished:
		if run.WorkflowVersionID == nil {
			return nil, domain.Graph{}, nil, ErrRunSnapshotUnsupported
		}
		workflow, err := store.GetWorkflow(ctx, run.WorkflowID)
		if err != nil {
			return nil, domain.Graph{}, nil, fmt.Errorf("%w: load workflow: %v", ErrRunSnapshotUnsupported, err)
		}
		_, version, err := store.GetAgentVersion(ctx, workflow.Slug, *run.WorkflowVersionID)
		if err != nil {
			return nil, domain.Graph{}, nil, fmt.Errorf("%w: load workflow version: %v", ErrRunSnapshotUnsupported, err)
		}
		raw = version.Graph
	default:
		return nil, domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	var graph domain.Graph
	if len(raw) == 0 || json.Unmarshal(raw, &graph) != nil {
		return nil, domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	plan, issues := compiler.Compile(graph)
	if len(issues) > 0 || plan == nil {
		return nil, domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	return append(json.RawMessage(nil), raw...), graph, plan, nil
}

func (service *DebugService) loadCompleteHistory(ctx context.Context, run domain.Run) ([]domain.RunEvent, error) {
	if run.Status != domain.RunCompleted && run.Status != domain.RunFailed && run.Status != domain.RunCancelled {
		return nil, errIncompleteRunHistory
	}
	events := make([]domain.RunEvent, 0)
	after := int64(0)
	for {
		page, err := service.store.ListRunEvents(ctx, run.ID, after, 200)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, event := range page {
			if event.Sequence != int64(len(events)+1) {
				return nil, errIncompleteRunHistory
			}
			events = append(events, event)
		}
		next := page[len(page)-1].Sequence
		if next <= after {
			return nil, errIncompleteRunHistory
		}
		after = next
		if len(page) < 200 {
			break
		}
	}
	return service.validateCompleteHistory(run, events)
}

type nodeAttemptKey struct {
	NodeID  string
	Attempt int
}

type attemptHistory struct {
	started   *domain.RunEvent
	terminal  *domain.RunEvent
	confirmed bool
}

func (service *DebugService) validateCompleteHistory(run domain.Run, events []domain.RunEvent) ([]domain.RunEvent, error) {
	if len(events) == 0 || (events[0].Type != "run.queued" && events[0].Type != "run.started") {
		return nil, errIncompleteRunHistory
	}
	wantTerminal := map[domain.RunStatus]string{
		domain.RunCompleted: "run.completed", domain.RunFailed: "run.failed", domain.RunCancelled: "run.cancelled",
	}[run.Status]
	if wantTerminal == "" || events[len(events)-1].Type != wantTerminal {
		return nil, errIncompleteRunHistory
	}
	runStarted := false
	attempts := make(map[nodeAttemptKey]attemptHistory)
	maxAttempt := make(map[string]int)
	for index := range events {
		event := &events[index]
		if event.RunID != run.ID || event.Sequence != int64(index+1) {
			return nil, errIncompleteRunHistory
		}
		switch event.Type {
		case "run.queued", "run.recovery_required":
			if event.NodeID != "" || event.NodeAttempt != nil {
				return nil, errIncompleteRunHistory
			}
		case "run.started":
			if runStarted || event.NodeID != "" || event.NodeAttempt != nil {
				return nil, errIncompleteRunHistory
			}
			runStarted = true
		case "run.completed", "run.failed", "run.cancelled":
			if !runStarted || index != len(events)-1 || event.Type != wantTerminal || event.NodeID != "" || event.NodeAttempt != nil {
				return nil, errIncompleteRunHistory
			}
		case "node.started", "node.completed", "node.failed", "node.skipped", "node.cancelled", "node.retry_confirmed":
			if !runStarted || event.NodeID == "" {
				return nil, errIncompleteRunHistory
			}
			attempt := historyEventAttempt(*event)
			if attempt < 1 || attempt > 3 || attempt < maxAttempt[event.NodeID] {
				return nil, errIncompleteRunHistory
			}
			key := nodeAttemptKey{NodeID: event.NodeID, Attempt: attempt}
			history := attempts[key]
			if attempt > maxAttempt[event.NodeID] && maxAttempt[event.NodeID] > 0 {
				previous := attempts[nodeAttemptKey{NodeID: event.NodeID, Attempt: maxAttempt[event.NodeID]}]
				if previous.terminal == nil && !previous.confirmed {
					return nil, errIncompleteRunHistory
				}
			}
			if attempt > maxAttempt[event.NodeID] {
				maxAttempt[event.NodeID] = attempt
			}
			switch event.Type {
			case "node.started":
				if history.started != nil || history.terminal != nil || history.confirmed {
					return nil, errIncompleteRunHistory
				}
				history.started = event
			case "node.skipped":
				if history.terminal != nil || history.confirmed {
					return nil, errIncompleteRunHistory
				}
				history.terminal = event
			case "node.completed", "node.failed":
				if history.started == nil || history.terminal != nil || history.confirmed {
					return nil, errIncompleteRunHistory
				}
				history.terminal = event
			case "node.cancelled":
				if history.terminal != nil || history.confirmed {
					return nil, errIncompleteRunHistory
				}
				history.terminal = event
			case "node.retry_confirmed":
				if history.started == nil || history.terminal != nil || history.confirmed {
					return nil, errIncompleteRunHistory
				}
				history.confirmed = true
			}
			attempts[key] = history
		default:
			return nil, errIncompleteRunHistory
		}
	}
	if !runStarted {
		return nil, errIncompleteRunHistory
	}
	return events, nil
}

func historyEventAttempt(event domain.RunEvent) int {
	if event.NodeAttempt == nil {
		return 1
	}
	return *event.NodeAttempt
}

func (service *DebugService) loadSourceChain(ctx context.Context, run domain.Run) ([]DebugSource, error) {
	chain := make([]DebugSource, 0)
	visited := map[string]struct{}{run.ID: {}}
	current := run
	for current.SourceRunID != nil {
		if len(chain) == 32 {
			return nil, ErrRunSnapshotUnsupported
		}
		if _, exists := visited[*current.SourceRunID]; exists {
			return nil, ErrRunSnapshotUnsupported
		}
		source, _, err := service.store.GetRun(ctx, *current.SourceRunID)
		if err != nil {
			return nil, fmt.Errorf("%w: load source run: %v", ErrRunSnapshotUnsupported, err)
		}
		nodeID := ""
		if current.SourceNodeID != nil {
			nodeID = *current.SourceNodeID
		}
		chain = append(chain, DebugSource{RunID: source.ID, SourceNodeID: nodeID, Mode: source.Mode, Status: source.Status})
		visited[source.ID] = struct{}{}
		current = source
	}
	if chain == nil {
		chain = []DebugSource{}
	}
	return chain, nil
}
