package workflow

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrRunRecoveryNotRequired        = errors.New("run recovery is not required")
	ErrRunRecoveryConflict           = errors.New("run recovery sequence conflict")
	ErrRunRecoveryNodeNotFound       = errors.New("run recovery node not found")
	ErrRunRecoveryRetryUnavailable   = errors.New("run recovery retry unavailable")
	ErrRunRecoveryRetryExhausted     = errors.New("run recovery retry exhausted")
	ErrRunRecoveryPayloadUnavailable = errors.New("run recovery payload unavailable")
)

type RunRecoveryNode struct {
	NodeID           string                    `json:"nodeId"`
	NodeType         string                    `json:"nodeType"`
	NodeTitle        string                    `json:"nodeTitle"`
	NodeAttempt      int                       `json:"nodeAttempt"`
	Safety           agentnode.ExecutionSafety `json:"safety"`
	StartedAt        time.Time                 `json:"startedAt"`
	RetryAllowed     bool                      `json:"retryAllowed"`
	RetryBlockReason string                    `json:"retryBlockReason,omitempty"`
	RiskMessage      string                    `json:"riskMessage"`
}

type RunRecoveryView struct {
	RunID       string                   `json:"runId"`
	Status      domain.RunStatus         `json:"status"`
	Reason      domain.RunRecoveryReason `json:"reason"`
	RequestedAt *time.Time               `json:"requestedAt,omitempty"`
	Sequence    int64                    `json:"sequence"`
	Nodes       []RunRecoveryNode        `json:"nodes"`
}

type ConfirmNodeRetryRequest struct {
	NodeAttempt      int   `json:"nodeAttempt"`
	ExpectedSequence int64 `json:"expectedSequence"`
}

type TerminateRecoveryRequest struct {
	ExpectedSequence int64 `json:"expectedSequence"`
}

type RunRecoveryStore interface {
	runSnapshotStore
	GetRun(context.Context, string) (domain.Run, []domain.NodeRun, error)
	ListRunEvents(context.Context, string, int64, int) ([]domain.RunEvent, error)
	ConfirmRunNodeRetry(context.Context, string, string, int, int64, bool) (domain.RunSummary, error)
	TerminateRunRecovery(context.Context, string, int64) (domain.RunSummary, error)
}

type RunRecoveryService struct {
	store    RunRecoveryStore
	compiler Compiler
}

func NewRunRecoveryService(store RunRecoveryStore, compiler Compiler) *RunRecoveryService {
	return &RunRecoveryService{store: store, compiler: compiler}
}

func (service *RunRecoveryService) Get(ctx context.Context, runID string) (RunRecoveryView, error) {
	runID, err := normalizeOptionalUUID(runID)
	if err != nil || runID == "" {
		return RunRecoveryView{}, ErrInvalidWorkflowInput
	}
	run, _, err := service.store.GetRun(ctx, runID)
	if err != nil {
		return RunRecoveryView{}, err
	}
	if run.Status != domain.RunRecoveryRequired {
		return RunRecoveryView{}, ErrRunRecoveryNotRequired
	}
	events, err := loadRecoveryEvents(ctx, service.store, run.ID)
	if err != nil {
		return RunRecoveryView{}, err
	}
	sequence, err := validateRecoveryHistory(run.ID, events)
	if err != nil {
		return RunRecoveryView{}, err
	}
	view := RunRecoveryView{
		RunID:       run.ID,
		Status:      run.Status,
		Reason:      run.RecoveryReason,
		RequestedAt: cloneRecoveryTime(run.RecoveryRequestedAt),
		Sequence:    sequence,
		Nodes:       []RunRecoveryNode{},
	}
	if !recoveryReasonHasRetryableNodes(run.RecoveryReason) {
		return view, nil
	}
	_, _, plan, err := loadRunGraphData(ctx, service.store, service.compiler, run)
	if err != nil {
		return RunRecoveryView{}, ErrRunRecoveryRetryUnavailable
	}
	view.Nodes = deriveRecoveryNodes(events, plan, run.RecoveryReason)
	return view, nil
}

func loadRecoveryEvents(ctx context.Context, store RunRecoveryStore, runID string) ([]domain.RunEvent, error) {
	events := make([]domain.RunEvent, 0)
	var after int64
	for {
		page, err := store.ListRunEvents(ctx, runID, after, 200)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		events = append(events, page...)
		next := page[len(page)-1].Sequence
		if next <= after {
			return nil, ErrRunRecoveryConflict
		}
		after = next
		if len(page) < 200 {
			break
		}
	}
	return events, nil
}

func (service *RunRecoveryService) ConfirmNodeRetry(ctx context.Context, runID, nodeID string, request ConfirmNodeRetryRequest) (domain.RunSummary, error) {
	if nodeID == "" || request.NodeAttempt < 1 || request.ExpectedSequence < 1 {
		return domain.RunSummary{}, ErrInvalidWorkflowInput
	}
	view, err := service.Get(ctx, runID)
	if err != nil {
		return domain.RunSummary{}, err
	}
	if request.ExpectedSequence != view.Sequence {
		return domain.RunSummary{}, ErrRunRecoveryConflict
	}
	if view.Reason == domain.RecoveryPayloadUnavailable {
		return domain.RunSummary{}, ErrRunRecoveryPayloadUnavailable
	}
	if !recoveryReasonHasRetryableNodes(view.Reason) {
		return domain.RunSummary{}, ErrRunRecoveryRetryUnavailable
	}
	var selected *RunRecoveryNode
	for index := range view.Nodes {
		if view.Nodes[index].NodeID == nodeID && view.Nodes[index].NodeAttempt == request.NodeAttempt {
			selected = &view.Nodes[index]
			break
		}
	}
	if selected == nil {
		return domain.RunSummary{}, ErrRunRecoveryNodeNotFound
	}
	if selected.NodeAttempt >= 3 {
		return domain.RunSummary{}, ErrRunRecoveryRetryExhausted
	}
	if !selected.RetryAllowed {
		return domain.RunSummary{}, ErrRunRecoveryRetryUnavailable
	}
	return service.store.ConfirmRunNodeRetry(ctx, view.RunID, nodeID, request.NodeAttempt, request.ExpectedSequence, len(view.Nodes) == 1)
}

func (service *RunRecoveryService) Terminate(ctx context.Context, runID string, request TerminateRecoveryRequest) (domain.RunSummary, error) {
	runID, err := normalizeOptionalUUID(runID)
	if err != nil || runID == "" || request.ExpectedSequence < 1 {
		return domain.RunSummary{}, ErrInvalidWorkflowInput
	}
	return service.store.TerminateRunRecovery(ctx, runID, request.ExpectedSequence)
}

func validateRecoveryHistory(runID string, events []domain.RunEvent) (int64, error) {
	if len(events) == 0 {
		return 0, ErrRunRecoveryConflict
	}
	recoverySeen := false
	for index, event := range events {
		if event.RunID != runID || event.Sequence != int64(index+1) {
			return 0, ErrRunRecoveryConflict
		}
		if event.Type == "run.recovery_required" {
			recoverySeen = true
			continue
		}
		if recoverySeen && event.Type != "node.retry_confirmed" {
			return 0, ErrRunRecoveryConflict
		}
	}
	if !recoverySeen {
		return 0, ErrRunRecoveryConflict
	}
	return events[len(events)-1].Sequence, nil
}

func recoveryReasonHasRetryableNodes(reason domain.RunRecoveryReason) bool {
	switch reason {
	case domain.RecoveryUncertainReadOnly, domain.RecoveryUncertainEffect, domain.RecoveryAttemptLimit:
		return true
	default:
		return false
	}
}

type recoveryAttemptKey struct {
	nodeID  string
	attempt int
}

func deriveRecoveryNodes(events []domain.RunEvent, plan *engine.Plan, reason domain.RunRecoveryReason) []RunRecoveryNode {
	started := make(map[recoveryAttemptKey]domain.RunEvent)
	resolved := make(map[recoveryAttemptKey]bool)
	for _, event := range events {
		if event.NodeID == "" || event.NodeAttempt == nil {
			continue
		}
		key := recoveryAttemptKey{nodeID: event.NodeID, attempt: *event.NodeAttempt}
		switch event.Type {
		case "node.started":
			started[key] = event
		case "node.completed", "node.failed", "node.skipped", "node.cancelled", "node.retry_confirmed":
			resolved[key] = true
		}
	}
	nodes := make([]RunRecoveryNode, 0, len(started))
	for key, event := range started {
		if resolved[key] {
			continue
		}
		compiled, ok := plan.Nodes[key.nodeID]
		safety := agentnode.ExecutionSafetySideEffect
		if ok {
			safety = agentnode.EffectiveExecutionSafety(compiled.ExecutionSafety)
		}
		if safety == agentnode.ExecutionSafetyPure && key.attempt < 3 && reason != domain.RecoveryAttemptLimit {
			continue
		}
		node := RunRecoveryNode{
			NodeID:       key.nodeID,
			NodeType:     compiled.Node.Type,
			NodeAttempt:  key.attempt,
			Safety:       safety,
			StartedAt:    event.Timestamp,
			RetryAllowed: ok && key.attempt < 3 && safety != agentnode.ExecutionSafetyPure && reason != domain.RecoveryAttemptLimit,
			RiskMessage:  recoveryRiskMessage(safety),
		}
		if ok && compiled.Executor != nil {
			node.NodeTitle = compiled.Executor.Definition().Title
		}
		if node.NodeTitle == "" {
			node.NodeTitle = node.NodeType
		}
		switch {
		case !ok:
			node.RetryBlockReason = "node_unavailable"
		case key.attempt >= 3 || reason == domain.RecoveryAttemptLimit:
			node.RetryBlockReason = "attempt_limit"
		case safety == agentnode.ExecutionSafetyPure:
			node.RetryBlockReason = "automatic_retry_only"
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(left, right int) bool {
		if nodes[left].NodeID == nodes[right].NodeID {
			return nodes[left].NodeAttempt < nodes[right].NodeAttempt
		}
		return nodes[left].NodeID < nodes[right].NodeID
	})
	return nodes
}

func recoveryRiskMessage(safety agentnode.ExecutionSafety) string {
	switch safety {
	case agentnode.ExecutionSafetyReadOnly:
		return "重新执行会再次读取外部数据，结果可能与上次不同。"
	case agentnode.ExecutionSafetySideEffect:
		return "重新执行可能重复调用外部服务、产生费用或写入数据，请确认后继续。"
	default:
		return "该节点由系统自动恢复，不能人工确认重试。"
	}
}

func cloneRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
