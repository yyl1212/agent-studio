package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
)

type workflowSnapshot struct {
	Descriptor   domain.WorkflowSnapshotDescriptor
	Graph        domain.Graph
	InputSchema  json.RawMessage
	Presentation domain.AgentPresentation
}

func (service *VersionGovernanceService) loadSnapshot(ctx context.Context, workflowID string, ref domain.WorkflowSnapshotRef) (workflowSnapshot, error) {
	switch ref.Kind {
	case domain.WorkflowSnapshotDraft:
		if ref.DraftRevision == nil || *ref.DraftRevision <= 0 || ref.Version != nil {
			return workflowSnapshot{}, ErrInvalidWorkflowInput
		}
		workflow, err := service.store.GetWorkflow(ctx, workflowID)
		if err != nil {
			return workflowSnapshot{}, err
		}
		if workflow.DraftRevision != *ref.DraftRevision {
			return workflowSnapshot{}, domain.ErrRevisionConflict
		}
		graph, err := decodeVersionSnapshotGraph(workflow.DraftGraph)
		if err != nil {
			return workflowSnapshot{}, domain.ErrWorkflowSnapshotUnsupported
		}
		revision := workflow.DraftRevision
		return workflowSnapshot{
			Descriptor:   domain.WorkflowSnapshotDescriptor{Kind: domain.WorkflowSnapshotDraft, DraftRevision: &revision},
			Graph:        graph,
			Presentation: workflow.AgentPresentation,
		}, nil
	case domain.WorkflowSnapshotVersion:
		if ref.Version == nil || *ref.Version <= 0 || ref.DraftRevision != nil {
			return workflowSnapshot{}, ErrInvalidWorkflowInput
		}
		version, err := service.store.GetWorkflowVersionByNumber(ctx, workflowID, *ref.Version)
		if err != nil {
			return workflowSnapshot{}, err
		}
		graph, err := decodeVersionSnapshotGraph(version.Graph)
		if err != nil {
			return workflowSnapshot{}, domain.ErrWorkflowSnapshotUnsupported
		}
		derivedSchema, err := deriveInputSchema(graph)
		if err != nil || !canonicalJSONEqual(derivedSchema, version.InputSchema) {
			return workflowSnapshot{}, domain.ErrWorkflowSnapshotUnsupported
		}
		versionNumber, versionID, createdAt := version.Version, version.ID, version.CreatedAt
		return workflowSnapshot{
			Descriptor: domain.WorkflowSnapshotDescriptor{
				Kind: domain.WorkflowSnapshotVersion, Version: &versionNumber,
				VersionID: &versionID, CreatedAt: &createdAt,
			},
			Graph:        graph,
			InputSchema:  append(json.RawMessage(nil), version.InputSchema...),
			Presentation: version.AgentPresentation,
		}, nil
	default:
		return workflowSnapshot{}, ErrInvalidWorkflowInput
	}
}

func decodeVersionSnapshotGraph(raw json.RawMessage) (domain.Graph, error) {
	if len(raw) == 0 || len(raw) > workflowtemplate.MaxTemplateBytes {
		return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var graph domain.Graph
	if err := decoder.Decode(&graph); err != nil {
		return domain.Graph{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
	}
	if graph.SchemaVersion != 1 {
		return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
	}
	nodeIDs := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if node.ID == "" {
			return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
		}
		if _, duplicate := nodeIDs[node.ID]; duplicate {
			return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
		}
		nodeIDs[node.ID] = struct{}{}
		config, err := decodeSnapshotJSON(node.Config)
		if err != nil || snapshotValueDepth(config, 1) > workflowtemplate.MaxDepth {
			return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
		}
	}
	edgeIDs := make(map[string]struct{}, len(graph.Edges))
	for _, edge := range graph.Edges {
		if edge.ID == "" {
			return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
		}
		if _, duplicate := edgeIDs[edge.ID]; duplicate {
			return domain.Graph{}, domain.ErrWorkflowSnapshotUnsupported
		}
		edgeIDs[edge.ID] = struct{}{}
	}
	if graph.Nodes == nil {
		graph.Nodes = []domain.Node{}
	}
	if graph.Edges == nil {
		graph.Edges = []domain.Edge{}
	}
	return graph, nil
}

func canonicalJSONEqual(left, right json.RawMessage) bool {
	leftValue, err := decodeSnapshotJSON(left)
	if err != nil {
		return false
	}
	rightValue, err := decodeSnapshotJSON(right)
	if err != nil {
		return false
	}
	return equalSnapshotJSON(leftValue, rightValue)
}

func decodeSnapshotJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, domain.ErrWorkflowSnapshotUnsupported
	}
	return value, nil
}

func equalSnapshotJSON(left, right any) bool {
	switch typedLeft := left.(type) {
	case nil:
		return right == nil
	case bool:
		typedRight, ok := right.(bool)
		return ok && typedLeft == typedRight
	case string:
		typedRight, ok := right.(string)
		return ok && typedLeft == typedRight
	case json.Number:
		typedRight, ok := right.(json.Number)
		if !ok {
			return false
		}
		leftNumber, leftOK := exactJSONNumber(typedLeft.String())
		rightNumber, rightOK := exactJSONNumber(typedRight.String())
		return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
	case []any:
		typedRight, ok := right.([]any)
		if !ok || len(typedLeft) != len(typedRight) {
			return false
		}
		for index := range typedLeft {
			if !equalSnapshotJSON(typedLeft[index], typedRight[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		typedRight, ok := right.(map[string]any)
		if !ok || len(typedLeft) != len(typedRight) {
			return false
		}
		for key, value := range typedLeft {
			rightValue, exists := typedRight[key]
			if !exists || !equalSnapshotJSON(value, rightValue) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func exactJSONNumber(raw string) (*big.Rat, bool) {
	mantissa, exponent := raw, 0
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		parsedExponent, err := strconv.Atoi(mantissa[index+1:])
		if err != nil || parsedExponent < -10000 || parsedExponent > 10000 {
			return nil, false
		}
		exponent = parsedExponent
		mantissa = mantissa[:index]
	}
	fractionDigits := 0
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		fractionDigits = len(mantissa) - index - 1
		mantissa = mantissa[:index] + mantissa[index+1:]
	}
	numerator, ok := new(big.Int).SetString(mantissa, 10)
	if !ok {
		return nil, false
	}
	denominator := big.NewInt(1)
	scale := fractionDigits - exponent
	if scale > 0 {
		denominator.Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	} else if scale < 0 {
		numerator.Mul(numerator, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-scale)), nil))
	}
	return new(big.Rat).SetFrac(numerator, denominator), true
}

func snapshotValueDepth(value any, depth int) int {
	maximum := depth
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			maximum = max(maximum, snapshotValueDepth(child, depth+1))
		}
	case []any:
		for _, child := range typed {
			maximum = max(maximum, snapshotValueDepth(child, depth+1))
		}
	}
	return maximum
}
