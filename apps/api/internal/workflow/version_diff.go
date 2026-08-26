package workflow

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type SemanticDiffEngine struct {
	definitions map[string]agentnode.Definition
}

func newSemanticDiffEngine(definitions []agentnode.Definition) *SemanticDiffEngine {
	indexed := make(map[string]agentnode.Definition, len(definitions))
	for _, definition := range definitions {
		indexed[definition.Type+"@"+definition.Version] = definition
	}
	return &SemanticDiffEngine{definitions: indexed}
}

func (engine *SemanticDiffEngine) Diff(base, compare workflowSnapshot) (domain.WorkflowDiff, error) {
	collector := newDiffCollector(base.Descriptor, compare.Descriptor)
	if err := engine.diffNodes(collector, base.Graph, compare.Graph); err != nil {
		return domain.WorkflowDiff{}, err
	}
	if err := engine.diffStartParameters(collector, base.Graph, compare.Graph); err != nil {
		return domain.WorkflowDiff{}, err
	}
	diffConnections(collector, base.Graph.Edges, compare.Graph.Edges)
	diffPresentation(collector, base.Presentation, compare.Presentation)
	engine.diffLayout(collector, base.Graph, compare.Graph)
	return collector.result, nil
}

type diffCollector struct {
	result domain.WorkflowDiff
}

func newDiffCollector(base, compare domain.WorkflowSnapshotDescriptor) *diffCollector {
	return &diffCollector{result: domain.WorkflowDiff{
		Base: base, Compare: compare,
		Groups: domain.WorkflowDiffGroups{
			Nodes:             []domain.WorkflowNodeDiff{},
			StartParameters:   []domain.WorkflowStartParameterDiff{},
			Connections:       []domain.WorkflowConnectionDiff{},
			AgentPresentation: []domain.WorkflowPresentationDiff{},
			Layout:            []domain.WorkflowLayoutDiff{},
		},
	}}
}

func (collector *diffCollector) addNode(detail domain.WorkflowNodeDiff, changes int) {
	collector.result.Summary.Nodes += changes
	collector.result.Summary.Total += changes
	collector.result.Groups.Nodes = append(collector.result.Groups.Nodes, detail)
}

func (collector *diffCollector) addStartParameter(detail domain.WorkflowStartParameterDiff, changes int) {
	collector.result.Summary.StartParameters += changes
	collector.result.Summary.Total += changes
	collector.result.Groups.StartParameters = append(collector.result.Groups.StartParameters, detail)
}

func (collector *diffCollector) addConnection(detail domain.WorkflowConnectionDiff) {
	collector.result.Summary.Connections++
	collector.result.Summary.Total++
	collector.result.Groups.Connections = append(collector.result.Groups.Connections, detail)
}

func (collector *diffCollector) addPresentation(detail domain.WorkflowPresentationDiff) {
	collector.result.Summary.AgentPresentation++
	collector.result.Summary.Total++
	collector.result.Groups.AgentPresentation = append(collector.result.Groups.AgentPresentation, detail)
}

func (collector *diffCollector) addLayout(detail domain.WorkflowLayoutDiff) {
	collector.result.Summary.Layout++
	collector.result.Summary.Total++
	collector.result.Groups.Layout = append(collector.result.Groups.Layout, detail)
}

func (engine *SemanticDiffEngine) diffNodes(collector *diffCollector, base, compare domain.Graph) error {
	baseNodes, compareNodes := nodeIndex(base.Nodes), nodeIndex(compare.Nodes)
	for _, nodeID := range unionSortedKeys(baseNodes, compareNodes) {
		before, beforeExists := baseNodes[nodeID]
		after, afterExists := compareNodes[nodeID]
		if !beforeExists {
			afterType := engine.nodeTypeSummary(after)
			collector.addNode(domain.WorkflowNodeDiff{
				NodeID: nodeID, Title: afterType.Title, Kind: domain.WorkflowDiffAdded,
				AfterType: &afterType, Config: []domain.WorkflowValueDiff{},
			}, 1)
			continue
		}
		if !afterExists {
			beforeType := engine.nodeTypeSummary(before)
			collector.addNode(domain.WorkflowNodeDiff{
				NodeID: nodeID, Title: beforeType.Title, Kind: domain.WorkflowDiffRemoved,
				BeforeType: &beforeType, Config: []domain.WorkflowValueDiff{},
			}, 1)
			continue
		}
		beforeType, afterType := engine.nodeTypeSummary(before), engine.nodeTypeSummary(after)
		detail := domain.WorkflowNodeDiff{
			NodeID: nodeID, Title: afterType.Title, Kind: domain.WorkflowDiffModified,
			BeforeType: &beforeType, AfterType: &afterType, Config: []domain.WorkflowValueDiff{},
		}
		changes := 0
		if before.Type != after.Type || before.TypeVersion != after.TypeVersion {
			changes++
		}
		beforeConfig, err := decodeSnapshotJSON(before.Config)
		if err != nil {
			return fmt.Errorf("decode base config for diff: %w", err)
		}
		afterConfig, err := decodeSnapshotJSON(after.Config)
		if err != nil {
			return fmt.Errorf("decode compare config for diff: %w", err)
		}
		if before.Type == "start" {
			beforeConfig = withoutObjectField(beforeConfig, "fields")
		}
		if after.Type == "start" {
			afterConfig = withoutObjectField(afterConfig, "fields")
		}
		detail.Config = append(detail.Config, diffJSONValues("/config", beforeConfig, true, afterConfig, true)...)
		changes += len(detail.Config)
		if changes > 0 {
			collector.addNode(detail, changes)
		}
	}
	return nil
}

func (engine *SemanticDiffEngine) nodeTypeSummary(node domain.Node) domain.WorkflowNodeTypeSummary {
	title := node.Type
	if definition, exists := engine.definitions[node.Type+"@"+node.TypeVersion]; exists && definition.Title != "" {
		title = definition.Title
	}
	return domain.WorkflowNodeTypeSummary{Type: node.Type, Version: node.TypeVersion, Title: title}
}

type startFieldState struct {
	value map[string]any
	order int
}

func (engine *SemanticDiffEngine) diffStartParameters(collector *diffCollector, base, compare domain.Graph) error {
	baseFields, baseRaw, baseValid, err := startFields(base)
	if err != nil {
		return err
	}
	compareFields, compareRaw, compareValid, err := startFields(compare)
	if err != nil {
		return err
	}
	if !baseValid || !compareValid {
		if !equalSnapshotJSON(baseRaw, compareRaw) {
			collector.addStartParameter(domain.WorkflowStartParameterDiff{
				Key: "(invalid)", Kind: domain.WorkflowDiffModified, Changes: []domain.WorkflowValueDiff{},
			}, 1)
		}
		return nil
	}
	for _, key := range unionSortedKeys(baseFields, compareFields) {
		before, beforeExists := baseFields[key]
		after, afterExists := compareFields[key]
		if !beforeExists {
			collector.addStartParameter(domain.WorkflowStartParameterDiff{
				Key: key, Kind: domain.WorkflowDiffAdded, AfterOrder: intPointer(after.order), Changes: []domain.WorkflowValueDiff{},
			}, 1)
			continue
		}
		if !afterExists {
			collector.addStartParameter(domain.WorkflowStartParameterDiff{
				Key: key, Kind: domain.WorkflowDiffRemoved, BeforeOrder: intPointer(before.order), Changes: []domain.WorkflowValueDiff{},
			}, 1)
			continue
		}
		changes := make([]domain.WorkflowValueDiff, 0)
		for _, field := range []string{"label", "description", "type", "required", "default", "placeholder", "options"} {
			beforeValue, beforeHas := before.value[field]
			afterValue, afterHas := after.value[field]
			if equalOptionalJSON(beforeValue, beforeHas, afterValue, afterHas) {
				continue
			}
			changes = append(changes, newValueDiff("/"+field, beforeValue, beforeHas, afterValue, afterHas))
		}
		if len(changes) > 0 {
			collector.addStartParameter(domain.WorkflowStartParameterDiff{
				Key: key, Kind: domain.WorkflowDiffModified,
				BeforeOrder: intPointer(before.order), AfterOrder: intPointer(after.order), Changes: changes,
			}, len(changes))
		}
		if before.order != after.order {
			collector.addStartParameter(domain.WorkflowStartParameterDiff{
				Key: key, Kind: domain.WorkflowDiffReordered,
				BeforeOrder: intPointer(before.order), AfterOrder: intPointer(after.order), Changes: []domain.WorkflowValueDiff{},
			}, 1)
		}
	}
	return nil
}

func startFields(graph domain.Graph) (map[string]startFieldState, any, bool, error) {
	nodes := append([]domain.Node(nil), graph.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, node := range nodes {
		if node.Type != "start" {
			continue
		}
		config, err := decodeSnapshotJSON(node.Config)
		if err != nil {
			return nil, nil, false, err
		}
		object, ok := config.(map[string]any)
		if !ok {
			return nil, config, false, nil
		}
		rawFields, exists := object["fields"]
		if !exists {
			return map[string]startFieldState{}, nil, true, nil
		}
		fields, ok := rawFields.([]any)
		if !ok {
			return nil, rawFields, false, nil
		}
		indexed := make(map[string]startFieldState, len(fields))
		for index, rawField := range fields {
			field, ok := rawField.(map[string]any)
			if !ok {
				return nil, rawFields, false, nil
			}
			key, ok := field["key"].(string)
			if !ok || key == "" {
				return nil, rawFields, false, nil
			}
			if _, duplicate := indexed[key]; duplicate {
				return nil, rawFields, false, nil
			}
			indexed[key] = startFieldState{value: field, order: index}
		}
		return indexed, rawFields, true, nil
	}
	return map[string]startFieldState{}, nil, true, nil
}

type connectionKey struct {
	source, sourcePort, target, targetPort string
}

func diffConnections(collector *diffCollector, base, compare []domain.Edge) {
	baseCounts, compareCounts := connectionCounts(base), connectionCounts(compare)
	keys := make([]connectionKey, 0, len(baseCounts)+len(compareCounts))
	seen := make(map[connectionKey]struct{}, len(baseCounts)+len(compareCounts))
	for key := range baseCounts {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range compareCounts {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return connectionKeyString(keys[i]) < connectionKeyString(keys[j]) })
	for _, key := range keys {
		delta := compareCounts[key] - baseCounts[key]
		kind := domain.WorkflowDiffAdded
		if delta < 0 {
			kind = domain.WorkflowDiffRemoved
			delta = -delta
		}
		for count := 0; count < delta; count++ {
			collector.addConnection(domain.WorkflowConnectionDiff{
				Kind: kind,
				Connection: domain.WorkflowConnectionSummary{
					Source: key.source, SourcePort: key.sourcePort, Target: key.target, TargetPort: key.targetPort,
				},
			})
		}
	}
}

func connectionCounts(edges []domain.Edge) map[connectionKey]int {
	counts := make(map[connectionKey]int, len(edges))
	for _, edge := range edges {
		key := connectionKey{edge.Source, edge.SourcePort, edge.Target, edge.TargetPort}
		counts[key]++
	}
	return counts
}

func connectionKeyString(key connectionKey) string {
	return key.source + "\x00" + key.sourcePort + "\x00" + key.target + "\x00" + key.targetPort
}

func diffPresentation(collector *diffCollector, base, compare domain.AgentPresentation) {
	fields := []struct {
		name        string
		beforeValue any
		afterValue  any
	}{
		{name: "title", beforeValue: base.Title, afterValue: compare.Title},
		{name: "description", beforeValue: base.Description, afterValue: compare.Description},
		{name: "accent", beforeValue: base.Accent, afterValue: compare.Accent},
		{name: "submitLabel", beforeValue: base.SubmitLabel, afterValue: compare.SubmitLabel},
		{name: "resultMode", beforeValue: base.ResultMode, afterValue: compare.ResultMode},
	}
	for _, field := range fields {
		if fmt.Sprint(field.beforeValue) == fmt.Sprint(field.afterValue) {
			continue
		}
		collector.addPresentation(domain.WorkflowPresentationDiff{
			Field:  field.name,
			Change: newValueDiff("/"+field.name, field.beforeValue, true, field.afterValue, true),
		})
	}
}

func (engine *SemanticDiffEngine) diffLayout(collector *diffCollector, base, compare domain.Graph) {
	baseNodes, compareNodes := nodeIndex(base.Nodes), nodeIndex(compare.Nodes)
	for _, nodeID := range unionSortedKeys(baseNodes, compareNodes) {
		before, beforeExists := baseNodes[nodeID]
		after, afterExists := compareNodes[nodeID]
		if !beforeExists || !afterExists {
			continue
		}
		beforePosition := quantizedPosition(before.Position)
		afterPosition := quantizedPosition(after.Position)
		if beforePosition == afterPosition {
			continue
		}
		collector.addLayout(domain.WorkflowLayoutDiff{
			NodeID: nodeID, Title: engine.nodeTypeSummary(after).Title,
			Before: beforePosition, After: afterPosition,
		})
	}
}

func quantizedPosition(position domain.Position) domain.Position {
	return domain.Position{X: math.Round(position.X*10) / 10, Y: math.Round(position.Y*10) / 10}
}

func diffJSONValues(path string, before any, beforeExists bool, after any, afterExists bool) []domain.WorkflowValueDiff {
	if equalOptionalJSON(before, beforeExists, after, afterExists) {
		return nil
	}
	beforeObject, beforeIsObject := before.(map[string]any)
	afterObject, afterIsObject := after.(map[string]any)
	if beforeExists && afterExists && beforeIsObject && afterIsObject {
		changes := make([]domain.WorkflowValueDiff, 0)
		for _, key := range unionSortedKeys(beforeObject, afterObject) {
			beforeValue, beforeHas := beforeObject[key]
			afterValue, afterHas := afterObject[key]
			changes = append(changes, diffJSONValues(path+"/"+escapeJSONPointerToken(key), beforeValue, beforeHas, afterValue, afterHas)...)
		}
		return changes
	}
	beforeArray, beforeIsArray := before.([]any)
	afterArray, afterIsArray := after.([]any)
	if beforeExists && afterExists && beforeIsArray && afterIsArray {
		changes := make([]domain.WorkflowValueDiff, 0)
		length := max(len(beforeArray), len(afterArray))
		for index := 0; index < length; index++ {
			beforeHas, afterHas := index < len(beforeArray), index < len(afterArray)
			var beforeValue, afterValue any
			if beforeHas {
				beforeValue = beforeArray[index]
			}
			if afterHas {
				afterValue = afterArray[index]
			}
			changes = append(changes, diffJSONValues(path+"/"+strconv.Itoa(index), beforeValue, beforeHas, afterValue, afterHas)...)
		}
		return changes
	}
	return []domain.WorkflowValueDiff{newValueDiff(path, before, beforeExists, after, afterExists)}
}

func newValueDiff(path string, before any, beforeExists bool, after any, afterExists bool) domain.WorkflowValueDiff {
	kind := domain.WorkflowDiffModified
	if !beforeExists {
		kind = domain.WorkflowDiffAdded
	} else if !afterExists {
		kind = domain.WorkflowDiffRemoved
	}
	return domain.WorkflowValueDiff{
		Path: path, Kind: kind,
		Before: rawDiffValue(before, beforeExists), After: rawDiffValue(after, afterExists),
	}
}

func rawDiffValue(value any, exists bool) *json.RawMessage {
	if !exists {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	raw := json.RawMessage(encoded)
	return &raw
}

func equalOptionalJSON(before any, beforeExists bool, after any, afterExists bool) bool {
	return beforeExists == afterExists && (!beforeExists || equalSnapshotJSON(before, after))
}

func withoutObjectField(value any, field string) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	copy := make(map[string]any, len(object))
	for key, child := range object {
		if key != field {
			copy[key] = child
		}
	}
	return copy
}

func nodeIndex(nodes []domain.Node) map[string]domain.Node {
	indexed := make(map[string]domain.Node, len(nodes))
	for _, node := range nodes {
		indexed[node.ID] = node
	}
	return indexed
}

func unionSortedKeys[V any](left, right map[string]V) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	keys := make([]string, 0, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range right {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func intPointer(value int) *int {
	return &value
}
