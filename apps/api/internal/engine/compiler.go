package engine

import (
	"fmt"
	"sort"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
)

type Compiler struct {
	registry *nodes.Registry
}

func NewCompiler(registry *nodes.Registry) *Compiler {
	return &Compiler{registry: registry}
}

func (compiler *Compiler) Compile(graph domain.Graph) (*Plan, []domain.ValidationIssue) {
	issues := make([]domain.ValidationIssue, 0)
	if graph.SchemaVersion != 1 {
		issues = append(issues, issue("SCHEMA_VERSION_INVALID", "工作流 schemaVersion 必须为 1", "", "", "schemaVersion"))
	}

	nodeByID := make(map[string]domain.Node, len(graph.Nodes))
	for index, node := range graph.Nodes {
		path := fmt.Sprintf("nodes[%d].id", index)
		if node.ID == "" {
			issues = append(issues, issue("NODE_ID_INVALID", "节点 ID 不能为空", "", "", path))
			continue
		}
		if _, exists := nodeByID[node.ID]; exists {
			issues = append(issues, issue("NODE_ID_DUPLICATE", "节点 ID 重复", node.ID, "", path))
			continue
		}
		nodeByID[node.ID] = node
	}
	edgeByID := make(map[string]domain.Edge, len(graph.Edges))
	for index, edge := range graph.Edges {
		path := fmt.Sprintf("edges[%d].id", index)
		if edge.ID == "" {
			issues = append(issues, issue("EDGE_ID_INVALID", "边 ID 不能为空", "", "", path))
			continue
		}
		if _, exists := edgeByID[edge.ID]; exists {
			issues = append(issues, issue("EDGE_ID_DUPLICATE", "边 ID 重复", "", edge.ID, path))
			continue
		}
		edgeByID[edge.ID] = edge
	}

	compiledNodes := make(map[string]CompiledNode, len(nodeByID))
	for nodeID, node := range nodeByID {
		executor, err := compiler.registry.Get(node.Type, node.TypeVersion)
		if err != nil {
			issues = append(issues, issue("NODE_TYPE_NOT_FOUND", "节点类型或版本未注册", nodeID, "", "typeVersion"))
			continue
		}
		if err := compiler.registry.ValidateConfig(node.Type, node.TypeVersion, node.Config); err != nil {
			issues = append(issues, issue("NODE_CONFIG_INVALID", "节点配置不符合 Schema", nodeID, "", "config"))
			continue
		}
		ports, err := executor.Resolve(node.Config)
		if err != nil {
			issues = append(issues, issue("NODE_RESOLVE_FAILED", "节点动态端口解析失败", nodeID, "", "config"))
			continue
		}
		compiledNodes[nodeID] = CompiledNode{Node: node, Executor: executor, Ports: ports}
	}

	structuralOutgoing := make(map[string][]string, len(nodeByID))
	structuralIncoming := make(map[string][]string, len(nodeByID))
	incoming := make(map[string][]domain.Edge, len(nodeByID))
	outgoing := make(map[string][]domain.Edge, len(nodeByID))
	portIncomingCount := make(map[string]int)
	edgeIDs := sortedEdgeIDs(edgeByID)
	for _, edgeID := range edgeIDs {
		edge := edgeByID[edgeID]
		_, sourceExists := nodeByID[edge.Source]
		_, targetExists := nodeByID[edge.Target]
		if !sourceExists || !targetExists {
			issues = append(issues, issue("EDGE_NODE_NOT_FOUND", "边引用了不存在的节点", "", edge.ID, ""))
			continue
		}
		if edge.Source == edge.Target {
			issues = append(issues, issue("EDGE_SELF_LOOP", "工作流不允许自连接", edge.Source, edge.ID, ""))
			continue
		}
		structuralOutgoing[edge.Source] = append(structuralOutgoing[edge.Source], edge.Target)
		structuralIncoming[edge.Target] = append(structuralIncoming[edge.Target], edge.Source)

		sourceNode, sourceCompiled := compiledNodes[edge.Source]
		targetNode, targetCompiled := compiledNodes[edge.Target]
		if !sourceCompiled || !targetCompiled {
			continue
		}
		sourcePort, sourcePortExists := findPort(sourceNode.Ports.Outputs, edge.SourcePort)
		targetPort, targetPortExists := findPort(targetNode.Ports.Inputs, edge.TargetPort)
		if !sourcePortExists {
			issues = append(issues, issue("SOURCE_PORT_NOT_FOUND", "源端口不存在", edge.Source, edge.ID, "sourcePort"))
		}
		if !targetPortExists {
			issues = append(issues, issue("TARGET_PORT_NOT_FOUND", "目标端口不存在", edge.Target, edge.ID, "targetPort"))
		}
		if !sourcePortExists || !targetPortExists {
			continue
		}
		if !compatibleTypes(sourcePort.Type, targetPort.Type) {
			issues = append(issues, issue("PORT_TYPE_MISMATCH", "端口数据类型不兼容", edge.Target, edge.ID, "targetPort"))
			continue
		}
		portKey := edge.Target + "\x00" + edge.TargetPort
		portIncomingCount[portKey]++
		if targetPort.Cardinality == domain.CardinalityOne && portIncomingCount[portKey] > 1 {
			issues = append(issues, issue("PORT_CARDINALITY_VIOLATION", "普通输入端口最多允许一条入边", edge.Target, edge.ID, "targetPort"))
			continue
		}
		incoming[edge.Target] = append(incoming[edge.Target], edge)
		outgoing[edge.Source] = append(outgoing[edge.Source], edge)
	}
	for nodeID, compiledNode := range compiledNodes {
		for _, input := range compiledNode.Ports.Inputs {
			if !input.Required {
				continue
			}
			portKey := nodeID + "\x00" + input.Key
			if portIncomingCount[portKey] == 0 {
				issues = append(issues, issue(
					"PORT_REQUIRED_CONNECTION_MISSING",
					"必填输入端口缺少连线",
					nodeID,
					"",
					"inputs."+input.Key,
				))
			}
		}
	}

	startIDs, endIDs := workflowBoundaryIDs(nodeByID)
	if len(startIDs) != 1 {
		issues = append(issues, issue("WORKFLOW_START_COUNT", "工作流必须恰有一个开始节点", "", "", "nodes"))
	}
	if len(endIDs) != 1 {
		issues = append(issues, issue("WORKFLOW_END_COUNT", "工作流必须恰有一个结束节点", "", "", "nodes"))
	}
	if len(startIDs) == 1 {
		reachable := traverse(startIDs[0], structuralOutgoing)
		for nodeID := range nodeByID {
			if !reachable[nodeID] {
				issues = append(issues, issue("NODE_UNREACHABLE_FROM_START", "节点无法从开始节点到达", nodeID, "", ""))
			}
		}
	}
	if len(endIDs) == 1 {
		canReachEnd := traverse(endIDs[0], structuralIncoming)
		for nodeID := range nodeByID {
			if !canReachEnd[nodeID] {
				issues = append(issues, issue("NODE_CANNOT_REACH_END", "节点不存在到结束节点的路径", nodeID, "", ""))
			}
		}
	}

	topologicalOrder, cyclic := topologicalSort(nodeByID, structuralOutgoing, structuralIncoming)
	if cyclic {
		issues = append(issues, issue("WORKFLOW_CYCLE", "工作流不允许循环", "", "", "edges"))
	}
	for nodeID := range nodeByID {
		sort.Slice(incoming[nodeID], func(i, j int) bool { return incoming[nodeID][i].ID < incoming[nodeID][j].ID })
		sort.Slice(outgoing[nodeID], func(i, j int) bool { return outgoing[nodeID][i].ID < outgoing[nodeID][j].ID })
	}

	sortIssues(issues)
	if len(issues) > 0 {
		return nil, issues
	}
	return &Plan{
		Graph:            graph,
		Nodes:            compiledNodes,
		Incoming:         incoming,
		Outgoing:         outgoing,
		TopologicalOrder: topologicalOrder,
		StartNodeID:      startIDs[0],
		EndNodeID:        endIDs[0],
	}, nil
}

func findPort(ports []domain.PortDefinition, key string) (domain.PortDefinition, bool) {
	for _, port := range ports {
		if port.Key == key {
			return port, true
		}
	}
	return domain.PortDefinition{}, false
}

func compatibleTypes(source, target domain.DataType) bool {
	return source == domain.TypeAny || target == domain.TypeAny || source == target
}

func workflowBoundaryIDs(nodeByID map[string]domain.Node) ([]string, []string) {
	starts := make([]string, 0, 1)
	ends := make([]string, 0, 1)
	for nodeID, node := range nodeByID {
		switch node.Type {
		case "start":
			starts = append(starts, nodeID)
		case "end":
			ends = append(ends, nodeID)
		}
	}
	sort.Strings(starts)
	sort.Strings(ends)
	return starts, ends
}

func traverse(start string, adjacency map[string][]string) map[string]bool {
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

func topologicalSort(nodesByID map[string]domain.Node, outgoing, incoming map[string][]string) ([]string, bool) {
	indegree := make(map[string]int, len(nodesByID))
	ready := make([]string, 0, len(nodesByID))
	for nodeID := range nodesByID {
		indegree[nodeID] = len(incoming[nodeID])
		if indegree[nodeID] == 0 {
			ready = append(ready, nodeID)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(nodesByID))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		for _, next := range outgoing[current] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
				sort.Strings(ready)
			}
		}
	}
	return order, len(order) != len(nodesByID)
}

func sortedEdgeIDs(edges map[string]domain.Edge) []string {
	ids := make([]string, 0, len(edges))
	for edgeID := range edges {
		ids = append(ids, edgeID)
	}
	sort.Strings(ids)
	return ids
}
