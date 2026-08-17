package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type fixtureNode struct {
	definition agentnode.Definition
	resolve    func(json.RawMessage) (agentnode.ResolvedPorts, error)
	execute    func(context.Context, agentnode.Request) (agentnode.Result, error)
}

func (node fixtureNode) Definition() agentnode.Definition {
	return node.definition
}

func (node fixtureNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if node.resolve != nil {
		return node.resolve(config)
	}
	return agentnode.ResolvedPorts{Inputs: node.definition.Inputs, Outputs: node.definition.Outputs}, nil
}

func (node fixtureNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if node.execute != nil {
		return node.execute(ctx, request)
	}
	return agentnode.Result{Outputs: map[string]any{"result": "ok"}}, nil
}

func TestValidateDefinitionRejectsInvalidIdentityAndDuplicates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentnode.Definition)
	}{
		{name: "empty type", mutate: func(definition *agentnode.Definition) { definition.Type = "" }},
		{name: "invalid type", mutate: func(definition *agentnode.Definition) { definition.Type = "Example_Echo" }},
		{name: "invalid version", mutate: func(definition *agentnode.Definition) { definition.Version = "v1" }},
		{name: "duplicate input", mutate: func(definition *agentnode.Definition) {
			definition.Inputs = append(definition.Inputs, definition.Inputs[0])
		}},
		{name: "duplicate output", mutate: func(definition *agentnode.Definition) {
			definition.Outputs = append(definition.Outputs, definition.Outputs[0])
		}},
		{name: "duplicate capability", mutate: func(definition *agentnode.Definition) {
			definition.Capabilities = []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilityNetwork}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validDefinition()
			test.mutate(&definition)
			if err := validateDefinition(definition); err == nil {
				t.Fatal("expected definition validation error")
			}
		})
	}
}

func TestValidateDefinitionRejectsInvalidDraft2020Schema(t *testing.T) {
	raw, err := os.ReadFile("testdata/invalid-schemas.json")
	if err != nil {
		t.Fatal(err)
	}
	definition := validDefinition()
	definition.ConfigSchema = raw
	if err := validateDefinition(definition); err == nil {
		t.Fatal("expected schema compilation error")
	}
}

func TestValidateConfigCasesRequiresConfigErrorsAndValidDynamicPorts(t *testing.T) {
	node := fixtureNode{
		definition: validDefinition(),
		resolve: func(config json.RawMessage) (agentnode.ResolvedPorts, error) {
			switch string(config) {
			case `{"valid":true}`:
				return agentnode.ResolvedPorts{Outputs: []agentnode.Port{{Key: "result"}}}, nil
			case `{"duplicate":true}`:
				return agentnode.ResolvedPorts{Outputs: []agentnode.Port{{Key: "result"}, {Key: "result"}}}, nil
			default:
				return agentnode.ResolvedPorts{}, agentnode.NewError(agentnode.ErrorKindConfig, "invalid_config", errors.New("bad config"), nil)
			}
		},
	}
	if err := validateConfigCases(node, []json.RawMessage{json.RawMessage(`{"valid":true}`)}, []json.RawMessage{json.RawMessage(`{"invalid":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := validateConfigCases(node, []json.RawMessage{json.RawMessage(`{"duplicate":true}`)}, nil); err == nil {
		t.Fatal("expected duplicate dynamic port error")
	}

	wrongKind := node
	wrongKind.resolve = func(json.RawMessage) (agentnode.ResolvedPorts, error) {
		return agentnode.ResolvedPorts{}, errors.New("unclassified")
	}
	if err := validateConfigCases(wrongKind, nil, []json.RawMessage{json.RawMessage(`{}`)}); err == nil {
		t.Fatal("expected invalid config error kind failure")
	}
}

func TestValidateExecutionRejectsInvalidOutputs(t *testing.T) {
	tests := []struct {
		name   string
		result agentnode.Result
		max    int
	}{
		{name: "undeclared output", result: agentnode.Result{Outputs: map[string]any{"other": true}}},
		{name: "undeclared active port", result: agentnode.Result{Outputs: map[string]any{"result": "ok"}, ActivePorts: []string{"other"}}},
		{name: "duplicate active port", result: agentnode.Result{Outputs: map[string]any{"result": "ok"}, ActivePorts: []string{"result", "result"}}},
		{name: "not JSON encodable", result: agentnode.Result{Outputs: map[string]any{"result": make(chan int)}}},
		{name: "too large", result: agentnode.Result{Outputs: map[string]any{"result": strings.Repeat("x", 64)}}, max: 32},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node := fixtureNode{definition: validDefinition(), execute: func(context.Context, agentnode.Request) (agentnode.Result, error) {
				return test.result, nil
			}}
			caseDefinition := ExecutionCase{Name: test.name, Request: agentnode.Request{}, WantOutputs: map[string]any{"result": "ok"}}
			if err := validateExecutionCase(node, caseDefinition, test.max); err == nil {
				t.Fatal("expected execution contract error")
			}
		})
	}
}

func TestValidateExecutionChecksExpectedOutputsAndErrorKind(t *testing.T) {
	node := fixtureNode{definition: validDefinition()}
	if err := validateExecutionCase(node, ExecutionCase{
		Name:        "success",
		WantOutputs: map[string]any{"result": "ok"},
	}, 0); err != nil {
		t.Fatal(err)
	}

	wantKind := agentnode.ErrorKindInput
	node.execute = func(context.Context, agentnode.Request) (agentnode.Result, error) {
		return agentnode.Result{}, agentnode.NewError(agentnode.ErrorKindInput, "missing_input", errors.New("missing"), nil)
	}
	if err := validateExecutionCase(node, ExecutionCase{Name: "input error", WantErrorKind: &wantKind}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCancellationRejectsNodeThatIgnoresContext(t *testing.T) {
	release := make(chan struct{})
	node := fixtureNode{definition: validDefinition(), execute: func(context.Context, agentnode.Request) (agentnode.Result, error) {
		<-release
		return agentnode.Result{}, nil
	}}
	err := validateCancellation(node, CancellationCase{CancelAfter: time.Millisecond, MaxWait: 10 * time.Millisecond})
	close(release)
	if err == nil {
		t.Fatal("expected cancellation contract error")
	}
}

func TestRunAcceptsValidContract(t *testing.T) {
	node := fixtureNode{
		definition: validDefinition(),
		execute: func(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
			if request.RunInput["wait"] == true {
				<-ctx.Done()
				return agentnode.Result{}, ctx.Err()
			}
			return agentnode.Result{Outputs: map[string]any{"result": "ok"}}, nil
		},
	}
	Run(t, Contract{
		Node:         node,
		ValidConfigs: []json.RawMessage{json.RawMessage(`{}`)},
		Executions: []ExecutionCase{{
			Name:        "success",
			WantOutputs: map[string]any{"result": "ok"},
		}},
		Cancellation: &CancellationCase{Request: agentnode.Request{RunInput: map[string]any{"wait": true}}},
	})
}

func validDefinition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "example.echo",
		Version:      "1.0.0",
		Title:        "Echo",
		ConfigSchema: agentnode.MustSchema(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`),
		Inputs: []agentnode.Port{{
			Key:         "text",
			Title:       "Text",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: []agentnode.Port{{
			Key:         "result",
			Title:       "Result",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
		Capabilities: []agentnode.Capability{agentnode.CapabilityNetwork},
	}
}
