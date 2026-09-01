package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
)

type Options struct {
	MaxParallel int
	Timeout     time.Duration
	Telemetry   observability.Providers
}

type Engine struct {
	maxParallel int
	timeout     time.Duration
	telemetry   *nodeTelemetry
}

const cancelledEventTimeout = time.Second

func New(options Options) *Engine {
	if options.MaxParallel <= 0 {
		options.MaxParallel = 4
	}
	if options.Timeout <= 0 {
		options.Timeout = 120 * time.Second
	}
	return &Engine{maxParallel: options.MaxParallel, timeout: options.Timeout, telemetry: newNodeTelemetry(options.Telemetry)}
}

func (engine *Engine) Run(ctx context.Context, runID string, plan *Plan, runInput map[string]any, observer Observer) (RunResult, error) {
	return engine.run(ctx, runID, plan, runInput, observer, nil, Checkpoint{})
}

func (engine *Engine) RunWithScope(ctx context.Context, runID string, plan *Plan, runInput map[string]any, observer Observer, scope ExecutionScope) (RunResult, error) {
	cloned := cloneExecutionScope(scope)
	if err := cloned.Validate(plan); err != nil {
		return RunResult{}, err
	}
	return engine.run(ctx, runID, plan, runInput, observer, &cloned, Checkpoint{})
}

func (engine *Engine) RunFromCheckpoint(ctx context.Context, runID string, plan *Plan, runInput map[string]any, observer Observer, checkpoint Checkpoint) (RunResult, error) {
	cloned := cloneCheckpoint(checkpoint)
	if err := cloned.Validate(plan); err != nil {
		return RunResult{}, err
	}
	return engine.run(ctx, runID, plan, runInput, observer, nil, cloned)
}

func (engine *Engine) run(ctx context.Context, runID string, plan *Plan, runInput map[string]any, observer Observer, scope *ExecutionScope, checkpoint Checkpoint) (RunResult, error) {
	if observer == nil {
		observer = discardObserver{}
	}
	if checkpoint.NodeStatuses == nil {
		checkpoint.NodeStatuses = map[string]domain.NodeStatus{}
	}
	if checkpoint.NodeAttempts == nil {
		checkpoint.NodeAttempts = map[string]int{}
	}
	if checkpoint.FrozenEdges == nil {
		checkpoint.FrozenEdges = map[string]FrozenEdge{}
	}
	runContext, cancel := context.WithTimeout(ctx, engine.timeout)
	defer cancel()
	startedAt := time.Now().UTC()
	activeNodeIDs := make(map[string]struct{}, len(plan.Nodes))
	if scope == nil {
		for nodeID := range plan.Nodes {
			activeNodeIDs[nodeID] = struct{}{}
		}
	} else {
		for nodeID := range scope.ActiveNodeIDs {
			activeNodeIDs[nodeID] = struct{}{}
		}
	}
	statuses := make(map[string]domain.NodeStatus, len(activeNodeIDs))
	for nodeID := range activeNodeIDs {
		status, restored := checkpoint.NodeStatuses[nodeID]
		if restored {
			statuses[nodeID] = status
		} else {
			statuses[nodeID] = domain.NodePending
		}
	}
	result := RunResult{RunID: runID, NodeStatuses: statuses, StartedAt: startedAt}
	sequence := checkpoint.LastSequence
	emit := func(event Event) error {
		sequence++
		event.Sequence = sequence
		event.RunID = runID
		event.Timestamp = time.Now().UTC()
		normalizeEventSlices(&event)
		return observer.Observe(runContext, event)
	}
	if !checkpoint.RunStarted {
		if err := emit(Event{Type: "run.started"}); err != nil {
			return finishResult(result), err
		}
	}

	edgeStates := make(map[string]edgeActivation, len(plan.Graph.Edges))
	edgeValues := make(map[string]any, len(plan.Graph.Edges))
	if scope != nil {
		for edgeID, frozen := range scope.FrozenEdges {
			if frozen.Active {
				edgeStates[edgeID] = edgeActive
				edgeValues[edgeID] = frozen.Value
			} else {
				edgeStates[edgeID] = edgeInactive
			}
		}
	}
	for edgeID, frozen := range checkpoint.FrozenEdges {
		if frozen.Active {
			edgeStates[edgeID] = edgeActive
			edgeValues[edgeID] = frozen.Value
		} else {
			edgeStates[edgeID] = edgeInactive
		}
	}
	workerResults := make(chan workerResult, len(activeNodeIDs))
	running := 0
	terminal := len(checkpoint.NodeStatuses)
	attempts := checkpoint.NodeAttempts
	var executionErr error

	for terminal < len(activeNodeIDs) {
		madeProgress := false
		for _, nodeID := range plan.TopologicalOrder {
			if _, active := activeNodeIDs[nodeID]; !active {
				continue
			}
			if statuses[nodeID] != domain.NodePending {
				continue
			}
			decision, inputs := nodeReadiness(plan, nodeID, edgeStates, edgeValues)
			if scope != nil && nodeID == scope.EntryNodeID {
				decision = nodeReady
				inputs = cloneNodeInputs(scope.EntryNodeInputs)
			}
			switch decision {
			case nodeWaiting:
				continue
			case nodeShouldSkip:
				attempt := attempts[nodeID] + 1
				if attempt > maxNodeAttempts {
					return finishResult(result), fmt.Errorf("node %q reached attempt limit", nodeID)
				}
				attempts[nodeID] = attempt
				statuses[nodeID] = domain.NodeSkipped
				terminal++
				madeProgress = true
				deactivateOutgoing(plan, nodeID, edgeStates)
				if err := emit(Event{Type: "node.skipped", NodeID: nodeID, NodeAttempt: attempt, Status: domain.NodeSkipped, Input: inputs}); err != nil {
					cancel()
					return finishResult(result), err
				}
			case nodeReady:
				if running >= engine.maxParallel {
					continue
				}
				attempt := attempts[nodeID] + 1
				if attempt > maxNodeAttempts {
					return finishResult(result), fmt.Errorf("node %q reached attempt limit", nodeID)
				}
				attempts[nodeID] = attempt
				statuses[nodeID] = domain.NodeRunning
				running++
				madeProgress = true
				eventInput := any(inputs)
				effectiveRunInput := runInput
				if scope != nil && nodeID == scope.EntryNodeID && nodeID == plan.StartNodeID {
					effectiveRunInput = scope.EntryRunInput
				}
				if nodeID == plan.StartNodeID {
					eventInput = effectiveRunInput
				}
				if err := emit(Event{Type: "node.started", NodeID: nodeID, NodeAttempt: attempt, Status: domain.NodeRunning, Input: eventInput}); err != nil {
					cancel()
					return finishResult(result), err
				}
				go executeNodeAttempt(runContext, plan, nodeID, attempt, effectiveRunInput, inputs, eventInput, engine.telemetry, workerResults)
			}
		}

		if terminal == len(activeNodeIDs) {
			break
		}
		if running == 0 {
			if !madeProgress {
				return finishResult(result), ErrSchedulerDeadlock
			}
			continue
		}

		select {
		case <-runContext.Done():
			for nodeID, status := range statuses {
				if status == domain.NodePending || status == domain.NodeRunning {
					statuses[nodeID] = domain.NodeCancelled
				}
			}
			ended := finishResult(result)
			cancelledEventContext, cancelCancelledEvent := context.WithTimeout(context.WithoutCancel(runContext), cancelledEventTimeout)
			_ = emitWithContext(cancelledEventContext, observer, &sequence, runID, Event{Type: "run.cancelled", Error: domain.NewPublicRunError(runContext.Err())})
			cancelCancelledEvent()
			return ended, runContext.Err()
		case worker := <-workerResults:
			running--
			if worker.err != nil {
				compiled := plan.Nodes[worker.nodeID]
				nodeErr := &NodeExecutionError{NodeID: worker.nodeID, NodeType: compiled.Node.Type, Err: worker.err}
				statuses[worker.nodeID] = domain.NodeFailed
				terminal++
				if executionErr == nil {
					executionErr = nodeErr
				}
				deactivateOutgoing(plan, worker.nodeID, edgeStates)
				publicError := domain.NewPublicNodeError(worker.err, worker.nodeID, compiled.Node.Type, compiled.Node.TypeVersion)
				if err := emit(Event{Type: "node.failed", NodeID: worker.nodeID, NodeAttempt: worker.attempt, Status: domain.NodeFailed, Input: worker.input, Error: publicError}); err != nil {
					cancel()
					return finishResult(result), err
				}
				descendants := descendantSet(plan, worker.nodeID)
				for _, nodeID := range plan.TopologicalOrder {
					if !descendants[nodeID] || statuses[nodeID] != domain.NodePending {
						continue
					}
					statuses[nodeID] = domain.NodeCancelled
					attempt := attempts[nodeID] + 1
					if attempt > maxNodeAttempts {
						return finishResult(result), fmt.Errorf("node %q reached attempt limit", nodeID)
					}
					attempts[nodeID] = attempt
					terminal++
					deactivateOutgoing(plan, nodeID, edgeStates)
					if err := emit(Event{Type: "node.cancelled", NodeID: nodeID, NodeAttempt: attempt, Status: domain.NodeCancelled}); err != nil {
						cancel()
						return finishResult(result), err
					}
				}
				continue
			}

			statuses[worker.nodeID] = domain.NodeCompleted
			terminal++
			applyNodeResult(plan, worker.nodeID, worker.result, edgeStates, edgeValues)
			if err := emit(Event{Type: "node.completed", NodeID: worker.nodeID, NodeAttempt: worker.attempt, Status: domain.NodeCompleted, Input: worker.input, Output: worker.result.Outputs, ActivePorts: append([]string(nil), worker.result.ActivePorts...)}); err != nil {
				cancel()
				return finishResult(result), err
			}
			if worker.nodeID == plan.EndNodeID {
				result.Output = worker.result.Outputs["result"]
			}
		}
	}

	result = finishResult(result)
	if executionErr != nil {
		publicError := domain.NewPublicRunError(executionErr)
		if err := emit(Event{Type: "run.failed", Error: publicError}); err != nil {
			return result, err
		}
		return result, executionErr
	}
	if statuses[plan.EndNodeID] != domain.NodeCompleted {
		return result, fmt.Errorf("%w: end node did not complete", ErrSchedulerDeadlock)
	}
	if err := emit(Event{Type: "run.completed", Output: result.Output}); err != nil {
		return result, err
	}
	return result, nil
}

func finishResult(result RunResult) RunResult {
	result.EndedAt = time.Now().UTC()
	return result
}

func emitWithContext(ctx context.Context, observer Observer, sequence *int64, runID string, event Event) error {
	*sequence++
	event.Sequence = *sequence
	event.RunID = runID
	event.Timestamp = time.Now().UTC()
	normalizeEventSlices(&event)
	observed := make(chan error, 1)
	go func() {
		observed <- observer.Observe(ctx, event)
	}()
	select {
	case err := <-observed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeEventSlices(event *Event) {
	if event.ActivePorts == nil {
		event.ActivePorts = []string{}
	}
	if event.InputRedactedPaths == nil {
		event.InputRedactedPaths = []string{}
	}
	if event.OutputRedactedPaths == nil {
		event.OutputRedactedPaths = []string{}
	}
}

func cloneNodeInputs(inputs map[string][]any) map[string][]any {
	cloned := make(map[string][]any, len(inputs))
	for key, values := range inputs {
		cloned[key] = append([]any(nil), values...)
	}
	return cloned
}
