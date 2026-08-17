package engine

import (
	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/nodes"
)

type CompiledNode struct {
	Node     domain.Node
	Executor nodes.NodeType
	Ports    domain.ResolvedPorts
}

type Plan struct {
	Graph            domain.Graph
	Nodes            map[string]CompiledNode
	Incoming         map[string][]domain.Edge
	Outgoing         map[string][]domain.Edge
	TopologicalOrder []string
	StartNodeID      string
	EndNodeID        string
}
