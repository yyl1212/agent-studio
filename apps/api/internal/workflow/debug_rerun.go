package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type rerunBuild struct {
	source   domain.Run
	rawGraph json.RawMessage
	plan     *engine.Plan
	input    map[string]any
	scope    engine.ExecutionScope
	preview  RerunPreview
}

func (service *DebugService) PreviewRerun(ctx context.Context, runID, nodeID string) (RerunPreview, error) {
	built, err := service.buildRerun(ctx, runID, nodeID)
	if err != nil {
		return RerunPreview{}, err
	}
	return built.preview, nil
}

func (service *DebugService) PrepareRerun(ctx context.Context, runID, nodeID string, request RerunRequest) (*PreparedRun, error) {
	built, err := service.buildRerun(ctx, runID, nodeID)
	if err != nil {
		return nil, err
	}
	if built.preview.RequiresConfirmation && !request.ConfirmSideEffects {
		return nil, ErrRunSideEffectConfirmationRequired
	}
	entryInput := normalizeInput(request.EntryInput)
	if containsHistoricRedactedPlaceholder(entryInput, built.preview.EntryInputRedactedPaths) {
		return nil, ErrRunEntryInputInvalid
	}
	if nodeID == built.plan.StartNodeID {
		schema, schemaErr := deriveInputSchema(built.plan.Graph)
		if schemaErr != nil || validateInput(schema, entryInput) != nil {
			return nil, ErrRunEntryInputInvalid
		}
		built.scope.EntryRunInput = cloneAnyMap(entryInput)
		built.scope.EntryNodeInputs = map[string][]any{}
	} else {
		nodeInputs, inputErr := validateNodeEntryInput(built.plan.Nodes[nodeID].Ports.Inputs, entryInput)
		if inputErr != nil {
			return nil, ErrRunEntryInputInvalid
		}
		built.scope.EntryRunInput = map[string]any{}
		built.scope.EntryNodeInputs = nodeInputs
	}
	inputJSON, err := json.Marshal(Redact(entryInput))
	if err != nil {
		return nil, fmt.Errorf("encode debug run input: %w", err)
	}
	runIDNew := uuid.NewString()
	sourceRunID, sourceNodeID := built.source.ID, nodeID
	run := domain.Run{
		ID:            runIDNew,
		WorkflowID:    built.source.WorkflowID,
		GraphSnapshot: append(json.RawMessage(nil), built.rawGraph...),
		SourceRunID:   &sourceRunID,
		SourceNodeID:  &sourceNodeID,
		Mode:          domain.RunModeDebug,
		Status:        domain.RunRunning,
		Input:         inputJSON,
		StartedAt:     time.Now().UTC(),
	}
	if err := service.store.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	return &PreparedRun{
		RunID:      runIDNew,
		Plan:       built.plan,
		Input:      cloneAnyMap(entryInput),
		Mode:       domain.RunModeDebug,
		WorkflowID: built.source.WorkflowID,
		Scope:      &built.scope,
	}, nil
}

func (service *DebugService) buildRerun(ctx context.Context, runID, nodeID string) (rerunBuild, error) {
	source, _, err := service.store.GetRun(ctx, runID)
	if err != nil {
		return rerunBuild{}, err
	}
	events, err := service.loadCompleteHistory(ctx, source)
	if err != nil {
		if errors.Is(err, errIncompleteRunHistory) {
			return rerunBuild{}, ErrRunReplayUnavailable
		}
		return rerunBuild{}, err
	}
	rawGraph, graph, plan, err := service.loadRunGraphData(ctx, source)
	if err != nil {
		return rerunBuild{}, err
	}
	if _, ok := plan.Nodes[nodeID]; !ok {
		return rerunBuild{}, ErrRunEntryInputInvalid
	}
	started, terminals := indexNodeHistory(events)
	entryStarts := started[nodeID]
	if len(entryStarts) != 1 {
		return rerunBuild{}, ErrRunEntryInputInvalid
	}
	entryInput := map[string]any{}
	if nodeID == plan.StartNodeID {
		if err := decodeJSON(source.Input, &entryInput); err != nil {
			return rerunBuild{}, ErrRunEntryInputInvalid
		}
	} else if err := decodeJSON(entryStarts[0].Input, &entryInput); err != nil {
		return rerunBuild{}, ErrRunEntryInputInvalid
	}
	active := descendants(plan, nodeID)
	scope := engine.ExecutionScope{
		EntryNodeID:     nodeID,
		ActiveNodeIDs:   active,
		EntryRunInput:   map[string]any{},
		EntryNodeInputs: map[string][]any{},
		FrozenEdges:     map[string]engine.FrozenEdge{},
	}
	preview := RerunPreview{
		SourceRunID:             source.ID,
		SourceNodeID:            nodeID,
		EntryInput:              cloneAnyMap(entryInput),
		EntryInputRedactedPaths: append([]string{}, entryStarts[0].InputRedactedPaths...),
		ActiveNodes:             []RerunNode{},
		FrozenEdges:             []FrozenEdgePreview{},
		EffectiveSafety:         agentnode.ExecutionSafetyPure,
	}
	for _, activeNodeID := range plan.TopologicalOrder {
		if _, ok := active[activeNodeID]; !ok {
			continue
		}
		compiled := plan.Nodes[activeNodeID]
		definition := compiled.Executor.Definition()
		safety := agentnode.EffectiveExecutionSafety(definition.ExecutionSafety)
		if safetyRank(safety) > safetyRank(preview.EffectiveSafety) {
			preview.EffectiveSafety = safety
		}
		preview.ActiveNodes = append(preview.ActiveNodes, RerunNode{ID: activeNodeID, Type: compiled.Node.Type, Version: compiled.Node.TypeVersion, Title: definition.Title, Safety: safety})
	}
	preview.RequiresConfirmation = preview.EffectiveSafety == agentnode.ExecutionSafetySideEffect
	for _, edge := range graph.Edges {
		_, sourceActive := active[edge.Source]
		_, targetActive := active[edge.Target]
		if sourceActive || !targetActive || edge.Target == nodeID {
			continue
		}
		terminal, ok := uniqueTerminal(terminals[edge.Source])
		if !ok {
			return rerunBuild{}, ErrRunFrozenEdgeUnavailable
		}
		frozen := engine.FrozenEdge{}
		if terminal.Type == "node.completed" {
			output := map[string]any{}
			if err := decodeJSON(terminal.Output, &output); err != nil {
				return rerunBuild{}, ErrRunFrozenEdgeUnavailable
			}
			value, exists := output[edge.SourcePort]
			frozen.Active = outputPortActive(terminal.ActivePorts, edge.SourcePort, exists)
			if frozen.Active {
				if !exists || pointerTouchesPort(terminal.OutputRedactedPaths, edge.SourcePort) {
					return rerunBuild{}, ErrRunFrozenEdgeUnavailable
				}
				frozen.Value = value
			}
		} else if terminal.Type != "node.skipped" && terminal.Type != "node.failed" && terminal.Type != "node.cancelled" {
			return rerunBuild{}, ErrRunFrozenEdgeUnavailable
		}
		scope.FrozenEdges[edge.ID] = frozen
		preview.FrozenEdges = append(preview.FrozenEdges, FrozenEdgePreview{
			EdgeID: edge.ID, Source: edge.Source, SourcePort: edge.SourcePort, Target: edge.Target, TargetPort: edge.TargetPort,
			Active: frozen.Active, Value: frozen.Value,
		})
	}
	if err := scope.Validate(plan); err != nil {
		return rerunBuild{}, ErrRunFrozenEdgeUnavailable
	}
	return rerunBuild{source: source, rawGraph: rawGraph, plan: plan, input: entryInput, scope: scope, preview: preview}, nil
}

func indexNodeHistory(events []domain.RunEvent) (map[string][]domain.RunEvent, map[string][]domain.RunEvent) {
	started := make(map[string][]domain.RunEvent)
	terminals := make(map[string][]domain.RunEvent)
	for _, event := range events {
		switch event.Type {
		case "node.started":
			started[event.NodeID] = append(started[event.NodeID], event)
		case "node.completed", "node.skipped", "node.failed", "node.cancelled":
			terminals[event.NodeID] = append(terminals[event.NodeID], event)
		}
	}
	return started, terminals
}

func uniqueTerminal(events []domain.RunEvent) (domain.RunEvent, bool) {
	if len(events) != 1 {
		return domain.RunEvent{}, false
	}
	return events[0], true
}

func descendants(plan *engine.Plan, entry string) map[string]struct{} {
	active := map[string]struct{}{entry: {}}
	queue := []string{entry}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range plan.Outgoing[current] {
			if _, exists := active[edge.Target]; exists {
				continue
			}
			active[edge.Target] = struct{}{}
			queue = append(queue, edge.Target)
		}
	}
	return active
}

func outputPortActive(activePorts []string, port string, exists bool) bool {
	if len(activePorts) == 0 {
		return exists
	}
	for _, active := range activePorts {
		if active == port {
			return true
		}
	}
	return false
}

func pointerTouchesPort(paths []string, port string) bool {
	prefix := "/" + strings.ReplaceAll(strings.ReplaceAll(port, "~", "~0"), "/", "~1")
	for _, path := range paths {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func safetyRank(safety agentnode.ExecutionSafety) int {
	switch safety {
	case agentnode.ExecutionSafetyReadOnly:
		return 1
	case agentnode.ExecutionSafetySideEffect:
		return 2
	default:
		return 0
	}
}

func validateNodeEntryInput(ports []domain.PortDefinition, input map[string]any) (map[string][]any, error) {
	byKey := make(map[string]domain.PortDefinition, len(ports))
	for _, port := range ports {
		byKey[port.Key] = port
	}
	normalized := make(map[string][]any, len(input))
	for key, raw := range input {
		port, exists := byKey[key]
		if !exists {
			return nil, ErrRunEntryInputInvalid
		}
		values, ok := raw.([]any)
		if !ok || len(values) > 1 {
			return nil, ErrRunEntryInputInvalid
		}
		for _, value := range values {
			if !validPortValue(port.Type, value) {
				return nil, ErrRunEntryInputInvalid
			}
		}
		normalized[key] = append([]any(nil), values...)
	}
	for _, port := range ports {
		if port.Required && len(normalized[port.Key]) != 1 {
			return nil, ErrRunEntryInputInvalid
		}
	}
	return normalized, nil
}

func validPortValue(dataType domain.DataType, value any) bool {
	switch dataType {
	case domain.TypeString:
		_, ok := value.(string)
		return ok
	case domain.TypeBoolean:
		_, ok := value.(bool)
		return ok
	case domain.TypeNumber:
		switch value.(type) {
		case json.Number, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	default:
		return true
	}
}

func decodeJSON(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("empty json")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func cloneAnyMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		if values, ok := value.([]any); ok {
			cloned[key] = append([]any(nil), values...)
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func containsHistoricRedactedPlaceholder(input map[string]any, paths []string) bool {
	for _, path := range paths {
		value, exists := jsonPointerValue(input, path)
		if exists && value == redactedValue {
			return true
		}
	}
	return false
}

func jsonPointerValue(value any, pointer string) (any, bool) {
	if pointer == "" {
		return value, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	current := value
	for _, encoded := range strings.Split(pointer[1:], "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		switch typed := current.(type) {
		case map[string]any:
			var exists bool
			current, exists = typed[token]
			if !exists {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}
