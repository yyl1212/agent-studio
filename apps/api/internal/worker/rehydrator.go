package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type RecoveryDecision struct {
	Required               bool
	Reason                 domain.RunRecoveryReason
	Sequence               int64
	Nodes                  []UncertainNode
	PayloadFailureCategory string
}

type UncertainNode struct {
	NodeID           string
	NodeAttempt      int
	Safety           agentnode.ExecutionSafety
	StartedAt        time.Time
	RetryAllowed     bool
	RetryBlockReason string
}

type PreparedExecution struct {
	Prepared       *workflow.PreparedRun
	Checkpoint     engine.Checkpoint
	Recovery       RecoveryDecision
	TerminalStatus domain.RunStatus
	TerminalOutput any
}

type Rehydrator struct {
	store    workflow.Store
	compiler workflow.Compiler
	cipher   *runpayload.Cipher
}

type nodeAttemptState struct {
	attempt   int
	started   bool
	terminal  bool
	confirmed bool
	status    domain.NodeStatus
	startedAt time.Time
}

func NewRehydrator(store workflow.Store, compiler workflow.Compiler, cipher *runpayload.Cipher) *Rehydrator {
	return &Rehydrator{store: store, compiler: compiler, cipher: cipher}
}

func (rehydrator *Rehydrator) Load(ctx context.Context, claimed workflow.ClaimedRun) (PreparedExecution, error) {
	if rehydrator.store == nil || rehydrator.compiler == nil || rehydrator.cipher == nil {
		return PreparedExecution{}, errors.New("rehydrator dependencies are incomplete")
	}
	run, events, payloads, err := rehydrator.store.LoadRunExecution(ctx, claimed.Run.ID)
	if err != nil {
		return PreparedExecution{}, err
	}
	if run.ID != claimed.Run.ID || run.LeaseOwner != claimed.Lease.Owner || run.LeaseToken != claimed.Lease.Token {
		return recoveryResult(nil, lastSequence(events), domain.RecoveryHistoryInvalid, nil), nil
	}
	input, err := rehydrator.decryptRunInput(run, payloads)
	if err != nil {
		return payloadRecoveryResult(nil, lastSequence(events), err), nil
	}
	prepared, err := workflow.LoadPreparedExecution(ctx, rehydrator.store, rehydrator.compiler, run, input)
	if err != nil {
		return recoveryResult(nil, lastSequence(events), domain.RecoveryNodeUnavailable, nil), nil
	}
	return rehydrator.RehydrateLoaded(ctx, run, events, payloads, prepared)
}

func (rehydrator *Rehydrator) RehydrateLoaded(_ context.Context, run domain.Run, events []domain.RunEvent, payloads []domain.RunPayload, prepared *workflow.PreparedRun) (PreparedExecution, error) {
	if rehydrator.cipher == nil || prepared == nil || prepared.Plan == nil {
		return PreparedExecution{}, errors.New("rehydrator execution dependencies are incomplete")
	}
	sequence := lastSequence(events)
	if run.ExecutionProtocol != domain.CurrentExecutionProtocol || domain.IsTerminalRunStatus(run.Status) {
		return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
	}
	payloadIndex, err := indexPayloads(run, payloads)
	if err != nil {
		return payloadRecoveryResult(prepared, sequence, err), nil
	}
	usedPayloads := make(map[payloadKey]bool, len(payloadIndex))
	input, err := decryptPayload[map[string]any](rehydrator.cipher, run, payloadIndex, usedPayloads, payloadKey{sequence: 0, kind: domain.RunPayloadInput}, "", 0)
	if err != nil {
		return payloadRecoveryResult(prepared, sequence, err), nil
	}
	prepared.Input = input

	checkpoint := engine.Checkpoint{NodeStatuses: map[string]domain.NodeStatus{}, NodeAttempts: map[string]int{}, FrozenEdges: map[string]engine.FrozenEdge{}}
	states := make(map[string]nodeAttemptState)
	runStarted := false
	var terminalStatus domain.RunStatus
	var terminalOutput any

	for index, event := range events {
		if event.RunID != run.ID || event.Sequence != int64(index+1) {
			return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
		}
		checkpoint.LastSequence = event.Sequence
		switch event.Type {
		case "run.queued", "run.recovery_required":
			if event.NodeID != "" || event.NodeAttempt != nil {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
		case "run.started":
			if runStarted || event.NodeID != "" || event.NodeAttempt != nil {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
			runStarted, checkpoint.RunStarted = true, true
		case "run.completed", "run.failed", "run.cancelled":
			if !runStarted || event.NodeID != "" || event.NodeAttempt != nil {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
			return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
		case "node.started", "node.completed", "node.failed", "node.skipped", "node.cancelled", "node.retry_confirmed":
			if !runStarted || event.NodeID == "" || event.NodeAttempt == nil || *event.NodeAttempt < 1 || *event.NodeAttempt > 3 {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
			compiled, exists := prepared.Plan.Nodes[event.NodeID]
			if !exists {
				return recoveryResult(prepared, sequence, domain.RecoveryNodeUnavailable, nil), nil
			}
			_ = compiled
			attempt := *event.NodeAttempt
			if !validNodeEventStatus(event.Type, event.Status) {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
			state := states[event.NodeID]
			if attempt < state.attempt || (attempt > state.attempt && state.attempt > 0 && !state.terminal && !state.confirmed && agentnode.EffectiveExecutionSafety(compiled.ExecutionSafety) != agentnode.ExecutionSafetyPure) {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
			if attempt > state.attempt {
				state = nodeAttemptState{attempt: attempt}
			}
			checkpoint.NodeAttempts[event.NodeID] = attempt
			switch event.Type {
			case "node.started":
				if state.started || state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				if _, err := decryptPayload[any](rehydrator.cipher, run, payloadIndex, usedPayloads, payloadKey{event.Sequence, domain.RunPayloadNodeInput}, event.NodeID, attempt); err != nil {
					return payloadRecoveryResult(prepared, sequence, err), nil
				}
				state.started, state.startedAt = true, event.Timestamp
			case "node.completed":
				if !state.started || state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				outputPayload, err := decryptPayload[runpayload.NodeOutputPayload](rehydrator.cipher, run, payloadIndex, usedPayloads, payloadKey{event.Sequence, domain.RunPayloadNodeOutput}, event.NodeID, attempt)
				if err != nil {
					return payloadRecoveryResult(prepared, sequence, err), nil
				}
				if outputPayload.Outputs == nil || outputPayload.ActivePorts == nil {
					return payloadRecoveryResult(prepared, sequence, errors.New("payload json invalid")), nil
				}
				state.terminal, state.status = true, domain.NodeCompleted
				freezeCompletedEdges(prepared.Plan, event.NodeID, outputPayload.Outputs, outputPayload.ActivePorts, checkpoint.FrozenEdges)
				if event.NodeID == prepared.Plan.EndNodeID {
					terminalStatus, terminalOutput = domain.RunCompleted, outputPayload.Outputs["result"]
				}
			case "node.skipped":
				if state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				state.terminal, state.status = true, domain.NodeSkipped
				freezeSkippedEdges(prepared.Plan, event.NodeID, checkpoint.FrozenEdges)
			case "node.failed":
				if !state.started || state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				state.terminal, state.status, terminalStatus = true, domain.NodeFailed, domain.RunFailed
			case "node.cancelled":
				if state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				state.terminal, state.status = true, domain.NodeCancelled
			case "node.retry_confirmed":
				if !state.started || state.terminal {
					return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
				}
				state.confirmed = true
			}
			states[event.NodeID] = state
		default:
			return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
		}
	}
	if len(events) == 0 || events[0].Type != "run.queued" {
		return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
	}
	for nodeID, state := range states {
		if state.terminal {
			if state.status == domain.NodeCompleted || state.status == domain.NodeSkipped {
				checkpoint.NodeStatuses[nodeID] = state.status
			}
			continue
		}
	}
	if terminalStatus == "" {
		for _, state := range states {
			if state.terminal && (state.status == domain.NodeFailed || state.status == domain.NodeCancelled) {
				return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
			}
		}
	}
	if len(usedPayloads) != len(payloadIndex) {
		return payloadRecoveryResult(prepared, sequence, errors.New("unexpected payload")), nil
	}
	if terminalStatus != "" {
		return PreparedExecution{Prepared: prepared, Checkpoint: checkpoint, TerminalStatus: terminalStatus, TerminalOutput: workflow.PublicRunOutput(prepared, terminalOutput)}, nil
	}
	uncertain := collectUncertainNodes(prepared.Plan, states)
	if len(uncertain) > 0 {
		reason := recoveryReasonForNodes(uncertain)
		return recoveryResult(prepared, sequence, reason, uncertain), nil
	}
	if err := checkpoint.Validate(prepared.Plan); err != nil {
		return recoveryResult(prepared, sequence, domain.RecoveryHistoryInvalid, nil), nil
	}
	return PreparedExecution{Prepared: prepared, Checkpoint: checkpoint}, nil
}

func validNodeEventStatus(eventType string, status domain.NodeStatus) bool {
	switch eventType {
	case "node.started":
		return status == domain.NodeRunning
	case "node.completed":
		return status == domain.NodeCompleted
	case "node.failed":
		return status == domain.NodeFailed
	case "node.skipped":
		return status == domain.NodeSkipped
	case "node.cancelled":
		return status == domain.NodeCancelled
	case "node.retry_confirmed":
		return status == ""
	default:
		return false
	}
}

type payloadKey struct {
	sequence int64
	kind     domain.RunPayloadKind
}

func indexPayloads(run domain.Run, payloads []domain.RunPayload) (map[payloadKey]domain.RunPayload, error) {
	result := make(map[payloadKey]domain.RunPayload, len(payloads))
	for _, payload := range payloads {
		key := payloadKey{payload.Sequence, payload.Kind}
		if payload.RunID != run.ID || payload.ExecutionProtocol != run.ExecutionProtocol || payload.CipherVersion != 1 || len(payload.Ciphertext) == 0 {
			return nil, errors.New("invalid payload metadata")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, errors.New("duplicate payload")
		}
		result[key] = payload
	}
	return result, nil
}

func decryptPayload[T any](cipher *runpayload.Cipher, run domain.Run, payloads map[payloadKey]domain.RunPayload, used map[payloadKey]bool, key payloadKey, nodeID string, nodeAttempt int) (T, error) {
	var zero T
	payload, ok := payloads[key]
	if !ok {
		return zero, errors.New("payload missing")
	}
	if payload.NodeID != nodeID || payload.NodeAttempt != nodeAttempt {
		return zero, errors.New("payload event metadata mismatch")
	}
	metadata := runpayload.Metadata{RunID: run.ID, Sequence: payload.Sequence, Kind: payload.Kind, NodeID: payload.NodeID, NodeAttempt: payload.NodeAttempt, ExecutionProtocol: payload.ExecutionProtocol}
	body, err := cipher.Open(metadata, payload.Ciphertext)
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(body, &zero); err != nil {
		return zero, errors.New("payload json invalid")
	}
	used[key] = true
	return zero, nil
}

func (rehydrator *Rehydrator) decryptRunInput(run domain.Run, payloads []domain.RunPayload) (map[string]any, error) {
	index, err := indexPayloads(run, payloads)
	if err != nil {
		return nil, err
	}
	return decryptPayload[map[string]any](rehydrator.cipher, run, index, map[payloadKey]bool{}, payloadKey{0, domain.RunPayloadInput}, "", 0)
}

func freezeCompletedEdges(plan *engine.Plan, nodeID string, outputs map[string]any, activePorts []string, frozen map[string]engine.FrozenEdge) {
	active := make(map[string]bool, len(activePorts)+len(outputs))
	if len(activePorts) == 0 {
		for port := range outputs {
			active[port] = true
		}
	} else {
		for _, port := range activePorts {
			active[port] = true
		}
	}
	for _, edge := range plan.Outgoing[nodeID] {
		value, exists := outputs[edge.SourcePort]
		frozen[edge.ID] = engine.FrozenEdge{Active: active[edge.SourcePort] && exists, Value: value}
	}
}

func freezeSkippedEdges(plan *engine.Plan, nodeID string, frozen map[string]engine.FrozenEdge) {
	for _, edge := range plan.Outgoing[nodeID] {
		frozen[edge.ID] = engine.FrozenEdge{}
	}
}

func collectUncertainNodes(plan *engine.Plan, states map[string]nodeAttemptState) []UncertainNode {
	nodes := make([]UncertainNode, 0)
	for nodeID, state := range states {
		if !state.started || state.terminal || state.confirmed {
			continue
		}
		safety := agentnode.EffectiveExecutionSafety(plan.Nodes[nodeID].ExecutionSafety)
		if safety == agentnode.ExecutionSafetyPure && state.attempt < 3 {
			continue
		}
		retryAllowed := state.attempt < 3
		blockReason := ""
		if !retryAllowed {
			blockReason = "attempt_limit_reached"
		}
		nodes = append(nodes, UncertainNode{NodeID: nodeID, NodeAttempt: state.attempt, Safety: safety, StartedAt: state.startedAt, RetryAllowed: retryAllowed, RetryBlockReason: blockReason})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes
}

func recoveryReasonForNodes(nodes []UncertainNode) domain.RunRecoveryReason {
	reason := domain.RecoveryUncertainReadOnly
	for _, node := range nodes {
		if !node.RetryAllowed {
			return domain.RecoveryAttemptLimit
		}
		if node.Safety == agentnode.ExecutionSafetySideEffect {
			reason = domain.RecoveryUncertainEffect
		}
	}
	return reason
}

func recoveryResult(prepared *workflow.PreparedRun, sequence int64, reason domain.RunRecoveryReason, nodes []UncertainNode) PreparedExecution {
	return PreparedExecution{Prepared: prepared, Recovery: RecoveryDecision{Required: true, Reason: reason, Sequence: sequence, Nodes: nodes}}
}

func payloadRecoveryResult(prepared *workflow.PreparedRun, sequence int64, err error) PreparedExecution {
	result := recoveryResult(prepared, sequence, domain.RecoveryPayloadUnavailable, nil)
	result.Recovery.PayloadFailureCategory = payloadFailureCategory(err)
	return result
}

func payloadFailureCategory(err error) string {
	switch {
	case errors.Is(err, runpayload.ErrAuthentication):
		return "authentication"
	case errors.Is(err, runpayload.ErrInvalidEnvelope):
		return "envelope"
	case err == nil:
		return "unknown"
	}
	switch err.Error() {
	case "payload missing":
		return "missing"
	case "payload json invalid":
		return "json"
	case "invalid payload metadata", "duplicate payload", "payload event metadata mismatch", "unexpected payload":
		return "metadata"
	default:
		return "unknown"
	}
}

func lastSequence(events []domain.RunEvent) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Sequence
}
