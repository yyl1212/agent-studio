package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	if nodeRuns == nil {
		nodeRuns = []domain.NodeRun{}
	}
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
	var raw json.RawMessage
	switch run.Mode {
	case domain.RunModeTest, domain.RunModeDebug:
		raw = run.GraphSnapshot
	case domain.RunModePublished:
		if run.WorkflowVersionID == nil {
			return domain.Graph{}, nil, ErrRunSnapshotUnsupported
		}
		workflow, err := service.store.GetWorkflow(ctx, run.WorkflowID)
		if err != nil {
			return domain.Graph{}, nil, fmt.Errorf("%w: load workflow: %v", ErrRunSnapshotUnsupported, err)
		}
		_, version, err := service.store.GetAgentVersion(ctx, workflow.Slug, *run.WorkflowVersionID)
		if err != nil {
			return domain.Graph{}, nil, fmt.Errorf("%w: load workflow version: %v", ErrRunSnapshotUnsupported, err)
		}
		raw = version.Graph
	default:
		return domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	var graph domain.Graph
	if len(raw) == 0 || json.Unmarshal(raw, &graph) != nil {
		return domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	plan, issues := service.compiler.Compile(graph)
	if len(issues) > 0 || plan == nil {
		return domain.Graph{}, nil, ErrRunSnapshotUnsupported
	}
	return graph, plan, nil
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
	if len(events) == 0 || events[0].Type != "run.started" {
		return nil, errIncompleteRunHistory
	}
	wantTerminal := map[domain.RunStatus]string{
		domain.RunCompleted: "run.completed", domain.RunFailed: "run.failed", domain.RunCancelled: "run.cancelled",
	}[run.Status]
	if events[len(events)-1].Type != wantTerminal {
		return nil, errIncompleteRunHistory
	}
	return events, nil
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
