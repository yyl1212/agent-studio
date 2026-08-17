package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type endNode struct{}

func NewEnd() *endNode {
	return &endNode{}
}

func (*endNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "end",
		Version:      "1",
		Title:        "结束",
		Description:  "返回 Agent 最终结果",
		Category:     "流程",
		ConfigSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Inputs: []agentnode.Port{{
			Key:         "result",
			Title:       "结果",
			Type:        agentnode.DataTypeAny,
			Required:    true,
			Cardinality: agentnode.CardinalitySingleActive,
		}},
		Outputs: []agentnode.Port{{
			Key:         "result",
			Title:       "最终结果",
			Type:        agentnode.DataTypeAny,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}

func (n *endNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	var parsed struct{}
	if err := decodeConfig(config, &parsed); err != nil {
		return agentnode.ResolvedPorts{}, nodeConfigError(err)
	}
	definition := n.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (*endNode) Execute(_ context.Context, request agentnode.Request) (agentnode.Result, error) {
	var parsed struct{}
	if err := decodeConfig(request.Config, &parsed); err != nil {
		return agentnode.Result{}, nodeConfigError(err)
	}
	results := request.Inputs["result"]
	switch len(results) {
	case 0:
		return agentnode.Result{}, nodeInputError(ErrEndResultMissing)
	case 1:
		return agentnode.Result{Outputs: map[string]any{"result": results[0]}}, nil
	default:
		return agentnode.Result{}, nodeInputError(fmt.Errorf("%w: got %d", ErrEndMultipleResults, len(results)))
	}
}
