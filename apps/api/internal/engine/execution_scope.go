package engine

import "fmt"

type FrozenEdge struct {
	Active bool
	Value  any
}

type ExecutionScope struct {
	EntryNodeID     string
	ActiveNodeIDs   map[string]struct{}
	EntryRunInput   map[string]any
	EntryNodeInputs map[string][]any
	FrozenEdges     map[string]FrozenEdge
}

func (scope ExecutionScope) Validate(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("execution scope: plan is nil")
	}
	if _, ok := plan.Nodes[scope.EntryNodeID]; !ok {
		return fmt.Errorf("execution scope: entry node %q not found", scope.EntryNodeID)
	}
	if _, ok := scope.ActiveNodeIDs[scope.EntryNodeID]; !ok {
		return fmt.Errorf("execution scope: entry node %q is not active", scope.EntryNodeID)
	}
	for nodeID := range scope.ActiveNodeIDs {
		if _, ok := plan.Nodes[nodeID]; !ok {
			return fmt.Errorf("execution scope: active node %q not found", nodeID)
		}
	}
	edges := make(map[string]struct {
		source string
		target string
	}, len(plan.Graph.Edges))
	for _, edge := range plan.Graph.Edges {
		edges[edge.ID] = struct {
			source string
			target string
		}{source: edge.Source, target: edge.Target}
	}
	for edgeID := range scope.FrozenEdges {
		edge, ok := edges[edgeID]
		if !ok {
			return fmt.Errorf("execution scope: frozen edge %q not found", edgeID)
		}
		_, sourceActive := scope.ActiveNodeIDs[edge.source]
		_, targetActive := scope.ActiveNodeIDs[edge.target]
		if sourceActive || !targetActive || edge.target == scope.EntryNodeID {
			return fmt.Errorf("execution scope: frozen edge %q has invalid boundary", edgeID)
		}
	}
	for nodeID := range scope.ActiveNodeIDs {
		if nodeID == scope.EntryNodeID {
			continue
		}
		for _, edge := range plan.Incoming[nodeID] {
			if _, sourceActive := scope.ActiveNodeIDs[edge.Source]; sourceActive {
				continue
			}
			if _, frozen := scope.FrozenEdges[edge.ID]; !frozen {
				return fmt.Errorf("execution scope: external edge %q is not frozen", edge.ID)
			}
		}
	}
	return nil
}

func cloneExecutionScope(scope ExecutionScope) ExecutionScope {
	cloned := ExecutionScope{
		EntryNodeID:     scope.EntryNodeID,
		ActiveNodeIDs:   make(map[string]struct{}, len(scope.ActiveNodeIDs)),
		EntryRunInput:   make(map[string]any, len(scope.EntryRunInput)),
		EntryNodeInputs: make(map[string][]any, len(scope.EntryNodeInputs)),
		FrozenEdges:     make(map[string]FrozenEdge, len(scope.FrozenEdges)),
	}
	for nodeID := range scope.ActiveNodeIDs {
		cloned.ActiveNodeIDs[nodeID] = struct{}{}
	}
	for key, value := range scope.EntryRunInput {
		cloned.EntryRunInput[key] = value
	}
	for key, values := range scope.EntryNodeInputs {
		cloned.EntryNodeInputs[key] = append([]any(nil), values...)
	}
	for edgeID, edge := range scope.FrozenEdges {
		cloned.FrozenEdges[edgeID] = edge
	}
	return cloned
}
