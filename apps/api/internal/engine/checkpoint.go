package engine

import (
	"fmt"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const maxNodeAttempts = 3

type Checkpoint struct {
	LastSequence int64
	RunStarted   bool
	NodeStatuses map[string]domain.NodeStatus
	NodeAttempts map[string]int
	FrozenEdges  map[string]FrozenEdge
}

func (checkpoint Checkpoint) Validate(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("checkpoint: plan is nil")
	}
	if checkpoint.LastSequence < 0 || (checkpoint.RunStarted && checkpoint.LastSequence == 0) {
		return fmt.Errorf("checkpoint: invalid last sequence %d", checkpoint.LastSequence)
	}
	if !checkpoint.RunStarted && (len(checkpoint.NodeStatuses) > 0 || len(checkpoint.NodeAttempts) > 0 || len(checkpoint.FrozenEdges) > 0) {
		return fmt.Errorf("checkpoint: node history exists before run.started")
	}
	for nodeID, attempt := range checkpoint.NodeAttempts {
		if _, ok := plan.Nodes[nodeID]; !ok {
			return fmt.Errorf("checkpoint: attempt references unknown node %q", nodeID)
		}
		if attempt < 1 || attempt > maxNodeAttempts {
			return fmt.Errorf("checkpoint: node %q has invalid attempt %d", nodeID, attempt)
		}
	}
	terminal := make(map[string]domain.NodeStatus, len(checkpoint.NodeStatuses))
	for nodeID, status := range checkpoint.NodeStatuses {
		if _, ok := plan.Nodes[nodeID]; !ok {
			return fmt.Errorf("checkpoint: status references unknown node %q", nodeID)
		}
		if status != domain.NodeCompleted && status != domain.NodeSkipped {
			return fmt.Errorf("checkpoint: node %q has unreduced status %q", nodeID, status)
		}
		if checkpoint.NodeAttempts[nodeID] == 0 {
			return fmt.Errorf("checkpoint: terminal node %q has no attempt", nodeID)
		}
		terminal[nodeID] = status
	}
	edges := make(map[string]domain.Edge, len(plan.Graph.Edges))
	for _, edge := range plan.Graph.Edges {
		edges[edge.ID] = edge
	}
	for edgeID, frozen := range checkpoint.FrozenEdges {
		edge, ok := edges[edgeID]
		if !ok {
			return fmt.Errorf("checkpoint: frozen edge %q not found", edgeID)
		}
		status, ok := terminal[edge.Source]
		if !ok {
			return fmt.Errorf("checkpoint: frozen edge %q source is not terminal", edgeID)
		}
		if status == domain.NodeSkipped && frozen.Active {
			return fmt.Errorf("checkpoint: skipped node edge %q cannot be active", edgeID)
		}
	}
	for nodeID := range terminal {
		for _, edge := range plan.Outgoing[nodeID] {
			if _, ok := checkpoint.FrozenEdges[edge.ID]; !ok {
				return fmt.Errorf("checkpoint: terminal node %q edge %q is not frozen", nodeID, edge.ID)
			}
		}
	}
	for nodeID, attempt := range checkpoint.NodeAttempts {
		if _, done := terminal[nodeID]; !done && attempt >= maxNodeAttempts {
			return fmt.Errorf("checkpoint: node %q reached attempt limit", nodeID)
		}
	}
	return nil
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	cloned := Checkpoint{
		LastSequence: checkpoint.LastSequence,
		RunStarted:   checkpoint.RunStarted,
		NodeStatuses: make(map[string]domain.NodeStatus, len(checkpoint.NodeStatuses)),
		NodeAttempts: make(map[string]int, len(checkpoint.NodeAttempts)),
		FrozenEdges:  make(map[string]FrozenEdge, len(checkpoint.FrozenEdges)),
	}
	for key, value := range checkpoint.NodeStatuses {
		cloned.NodeStatuses[key] = value
	}
	for key, value := range checkpoint.NodeAttempts {
		cloned.NodeAttempts[key] = value
	}
	for key, value := range checkpoint.FrozenEdges {
		cloned.FrozenEdges[key] = value
	}
	return cloned
}
