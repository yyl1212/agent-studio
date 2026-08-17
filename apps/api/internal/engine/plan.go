package engine

import (
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
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
