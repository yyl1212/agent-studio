package engine

import (
	"context"
	"fmt"
	"time"

	"agentstudio.local/api/internal/domain"
)

type Options struct {
	MaxParallel int
	Timeout     time.Duration
}

type Engine struct {
	maxParallel int
	timeout     time.Duration
}

func New(options Options) *Engine {
	if options.MaxParallel <= 0 {
		options.MaxParallel = 4
	}
	if options.Timeout <= 0 {
		options.Timeout = 120 * time.Second
	}
	return &Engine{maxParallel: options.MaxParallel, timeout: options.Timeout}
}

func (engine *Engine) Run(ctx context.Context, runID string, plan *Plan, runInput map[string]any, observer Observer) (RunResult, error) {
	if observer == nil {
		observer = discardObserver{}
	}
	runContext, cancel := context.WithTimeout(ctx, engine.timeout)
	defer cancel()
	startedAt := time.Now().UTC()
	statuses := make(map[string]domain.NodeStatus, len(plan.Nodes))
	for nodeID := range plan.Nodes {
		statuses[nodeID] = domain.NodePending
	}
	result := RunResult{RunID: runID, NodeStatuses: statuses, StartedAt: startedAt}
	sequence := int64(0)
	emit := func(event Event) error {
		sequence++
		event.Sequence = sequence
		event.RunID = runID
		event.Timestamp = time.Now().UTC()
		return observer.Observe(runContext, event)
	}
	if err := emit(Event{Type: "run.started"}); err != nil {
		return finishResult(result), err
	}

	edgeStates := make(map[string]edgeActivation, len(plan.Graph.Edges))
	edgeValues := make(map[string]any, len(plan.Graph.Edges))
	workerResults := make(chan workerResult, len(plan.Nodes))
	running := 0
	terminal := 0
	var executionErr error

	for terminal < len(plan.Nodes) {
		madeProgress := false
		for _, nodeID := range plan.TopologicalOrder {
			if statuses[nodeID] != domain.NodePending {
				continue
			}
			decision, inputs := nodeReadiness(plan, nodeID, edgeStates, edgeValues)
			switch decision {
			case nodeWaiting:
				continue
			case nodeShouldSkip:
				statuses[nodeID] = domain.NodeSkipped
				terminal++
				madeProgress = true
				deactivateOutgoing(plan, nodeID, edgeStates)
				if err := emit(Event{Type: "node.skipped", NodeID: nodeID, Status: domain.NodeSkipped, Input: inputs}); err != nil {
					cancel()
					return finishResult(result), err
				}
			case nodeReady:
				if running >= engine.maxParallel {
					continue
				}
				statuses[nodeID] = domain.NodeRunning
				running++
				madeProgress = true
				eventInput := any(inputs)
				if nodeID == plan.StartNodeID {
					eventInput = runInput
				}
				if err := emit(Event{Type: "node.started", NodeID: nodeID, Status: domain.NodeRunning, Input: eventInput}); err != nil {
					cancel()
					return finishResult(result), err
				}
				go executeNode(runContext, plan, nodeID, runInput, inputs, eventInput, workerResults)
			}
		}

		if terminal == len(plan.Nodes) {
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
			_ = emitWithContext(context.WithoutCancel(runContext), observer, &sequence, runID, Event{Type: "run.cancelled", Error: &domain.PublicError{Code: "RUN_CANCELLED", Message: "运行已取消"}})
			return ended, runContext.Err()
		case worker := <-workerResults:
			running--
			if worker.err != nil {
				statuses[worker.nodeID] = domain.NodeFailed
				terminal++
				if executionErr == nil {
					executionErr = worker.err
				}
				deactivateOutgoing(plan, worker.nodeID, edgeStates)
				publicError := &domain.PublicError{Code: "NODE_EXECUTION_FAILED", Message: "节点执行失败", NodeID: worker.nodeID}
				if err := emit(Event{Type: "node.failed", NodeID: worker.nodeID, Status: domain.NodeFailed, Input: worker.input, Error: publicError}); err != nil {
					cancel()
					return finishResult(result), err
				}
				descendants := descendantSet(plan, worker.nodeID)
				for _, nodeID := range plan.TopologicalOrder {
					if !descendants[nodeID] || statuses[nodeID] != domain.NodePending {
						continue
					}
					statuses[nodeID] = domain.NodeCancelled
					terminal++
					deactivateOutgoing(plan, nodeID, edgeStates)
					if err := emit(Event{Type: "node.cancelled", NodeID: nodeID, Status: domain.NodeCancelled}); err != nil {
						cancel()
						return finishResult(result), err
					}
				}
				continue
			}

			statuses[worker.nodeID] = domain.NodeCompleted
			terminal++
			applyNodeResult(plan, worker.nodeID, worker.result, edgeStates, edgeValues)
			if err := emit(Event{Type: "node.completed", NodeID: worker.nodeID, Status: domain.NodeCompleted, Input: worker.input, Output: worker.result.Outputs}); err != nil {
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
		publicError := &domain.PublicError{Code: "RUN_FAILED", Message: "运行失败"}
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
	return observer.Observe(ctx, event)
}
