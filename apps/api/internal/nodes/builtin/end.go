package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type endNode struct{}

func NewEnd() *endNode {
	return &endNode{}
}

func (*endNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:         "end",
		Version:      "1",
		Title:        "结束",
		Description:  "返回 Agent 最终结果",
		Category:     "流程",
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Inputs: []domain.PortDefinition{{
			Key:         "result",
			Title:       "结果",
			Type:        domain.TypeAny,
			Required:    true,
			Cardinality: domain.CardinalitySingleActive,
		}},
		Outputs: []domain.PortDefinition{{
			Key:         "result",
			Title:       "最终结果",
			Type:        domain.TypeAny,
			Cardinality: domain.CardinalityOne,
		}},
	}
}

func (n *endNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	definition := n.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (*endNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	results := request.Inputs["result"]
	switch len(results) {
	case 0:
		return domain.NodeResult{}, ErrEndResultMissing
	case 1:
		return domain.NodeResult{Outputs: map[string]any{"result": results[0]}}, nil
	default:
		return domain.NodeResult{}, fmt.Errorf("%w: got %d", ErrEndMultipleResults, len(results))
	}
}
