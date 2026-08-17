package engine

import (
	"context"
	"errors"

	"agentstudio.local/api/internal/domain"
)

var ErrSchedulerDeadlock = errors.New("workflow scheduler deadlock")

type edgeActivation uint8

const (
	edgeUnknown edgeActivation = iota
	edgeInactive
	edgeActive
)

type workerResult struct {
	nodeID string
	input  any
	result domain.NodeResult
	err    error
}

type readiness uint8

const (
	nodeWaiting readiness = iota
	nodeReady
	nodeShouldSkip
)

func nodeReadiness(plan *Plan, nodeID string, edgeStates map[string]edgeActivation, edgeValues map[string]any) (readiness, map[string][]any) {
	if nodeID == plan.StartNodeID {
		return nodeReady, map[string][]any{}
	}
	for _, edge := range plan.Incoming[nodeID] {
		if edgeStates[edge.ID] == edgeUnknown {
			return nodeWaiting, nil
		}
	}
	inputs := collectInputs(plan.Incoming[nodeID], edgeStates, edgeValues)
	if nodeID == plan.EndNodeID {
		return nodeReady, inputs
	}
	for _, port := range plan.Nodes[nodeID].Ports.Inputs {
		if port.Required && len(inputs[port.Key]) == 0 {
			return nodeShouldSkip, inputs
		}
	}
	return nodeReady, inputs
}

func collectInputs(edges []domain.Edge, edgeStates map[string]edgeActivation, edgeValues map[string]any) map[string][]any {
	inputs := make(map[string][]any)
	for _, edge := range edges {
		if edgeStates[edge.ID] == edgeActive {
			inputs[edge.TargetPort] = append(inputs[edge.TargetPort], edgeValues[edge.ID])
		}
	}
	return inputs
}

func applyNodeResult(plan *Plan, nodeID string, result domain.NodeResult, edgeStates map[string]edgeActivation, edgeValues map[string]any) {
	active := make(map[string]bool, len(result.ActivePorts)+len(result.Outputs))
	if len(result.ActivePorts) > 0 {
		for _, port := range result.ActivePorts {
			active[port] = true
		}
	} else {
		for port := range result.Outputs {
			active[port] = true
		}
	}
	for _, edge := range plan.Outgoing[nodeID] {
		value, exists := result.Outputs[edge.SourcePort]
		if active[edge.SourcePort] && exists {
			edgeStates[edge.ID] = edgeActive
			edgeValues[edge.ID] = value
		} else {
			edgeStates[edge.ID] = edgeInactive
		}
	}
}

func deactivateOutgoing(plan *Plan, nodeID string, edgeStates map[string]edgeActivation) {
	for _, edge := range plan.Outgoing[nodeID] {
		if edgeStates[edge.ID] == edgeUnknown {
			edgeStates[edge.ID] = edgeInactive
		}
	}
}

func descendantSet(plan *Plan, source string) map[string]bool {
	descendants := make(map[string]bool)
	queue := []string{source}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range plan.Outgoing[current] {
			if !descendants[edge.Target] {
				descendants[edge.Target] = true
				queue = append(queue, edge.Target)
			}
		}
	}
	delete(descendants, source)
	return descendants
}

func executeNode(ctx context.Context, plan *Plan, nodeID string, runInput map[string]any, inputs map[string][]any, eventInput any, results chan<- workerResult) {
	compiled := plan.Nodes[nodeID]
	result, err := compiled.Executor.Execute(ctx, domain.NodeRequest{
		Inputs:   inputs,
		RunInput: runInput,
		Config:   compiled.Node.Config,
	})
	results <- workerResult{nodeID: nodeID, input: eventInput, result: result, err: err}
}
