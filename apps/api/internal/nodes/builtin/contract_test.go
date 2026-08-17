package builtin

import (
	"encoding/json"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"github.com/yyl1212/agent-studio/sdk/go/agenttest"
)

func TestCoreNodeContracts(t *testing.T) {
	inputKind := agentnode.ErrorKindInput
	internalKind := agentnode.ErrorKindInternal
	tests := []struct {
		name     string
		contract agenttest.Contract
	}{
		{
			name: "start",
			contract: agenttest.Contract{
				Node: NewStart(),
				ValidConfigs: []json.RawMessage{
					json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
				},
				InvalidConfigs: []json.RawMessage{
					json.RawMessage(`{"fields":[{"key":"bad-key","label":"主题","type":"text"}]}`),
				},
				Executions: []agenttest.ExecutionCase{{
					Name: "emits run input",
					Request: agentnode.Request{
						Config:   json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
						RunInput: map[string]any{"topic": "Agent"},
					},
					WantOutputs: map[string]any{"topic": "Agent"},
				}, {
					Name: "classifies missing run input",
					Request: agentnode.Request{
						Config:   json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
						RunInput: map[string]any{},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
		{
			name: "template",
			contract: agenttest.Contract{
				Node:           NewTemplate(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{"template":"你好，{{name}}"}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"template":"你好，{{ name }}"}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "renders variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"你好，{{name}}"}`),
						Inputs: map[string][]any{"name": {"Codex"}},
					},
					WantOutputs: map[string]any{"text": "你好，Codex"},
				}, {
					Name: "classifies missing variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"你好，{{name}}"}`),
						Inputs: map[string][]any{},
					},
					WantErrorKind: &inputKind,
				}, {
					Name: "classifies non JSON variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"{{value}}"}`),
						Inputs: map[string][]any{"value": {make(chan int)}},
					},
					WantErrorKind: &internalKind,
				}},
			},
		},
		{
			name: "condition",
			contract: agenttest.Contract{
				Node:           NewCondition(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{"operator":"equals","compareValue":"yes"}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"operator":"unknown"}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "activates true branch",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"operator":"equals","compareValue":"yes"}`),
						Inputs: map[string][]any{"value": {"yes"}},
					},
					WantOutputs: map[string]any{"true": "yes"},
				}, {
					Name: "classifies incompatible comparison",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"operator":"greaterThan","compareValue":2}`),
						Inputs: map[string][]any{"value": {"three"}},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
		{
			name: "end",
			contract: agenttest.Contract{
				Node:           NewEnd(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"unknown":true}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "returns result",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{"result": {"done"}},
					},
					WantOutputs: map[string]any{"result": "done"},
				}, {
					Name: "classifies missing result",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agenttest.Run(t, test.contract)
		})
	}
}
