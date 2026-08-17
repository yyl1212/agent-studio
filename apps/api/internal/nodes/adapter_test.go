package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type adapterFixtureNode struct {
	definition agentnode.Definition
	ports      agentnode.ResolvedPorts
	result     agentnode.Result
}

func (node adapterFixtureNode) Definition() agentnode.Definition {
	return node.definition
}

func (node adapterFixtureNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	return node.ports, nil
}

func (node adapterFixtureNode) Execute(context.Context, agentnode.Request) (agentnode.Result, error) {
	return node.result, nil
}

func TestNormalizeDefinitionUsesJSONArraysForMissingPorts(t *testing.T) {
	definition, err := NormalizeDefinition(agentnode.Definition{
		Type:         "example.empty",
		Version:      "1",
		ConfigSchema: agentnode.MustSchema(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Inputs == nil || definition.Outputs == nil {
		t.Fatalf("ports must be non-nil: %#v", definition)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(document["inputs"], []any{}) || !reflect.DeepEqual(document["outputs"], []any{}) {
		t.Fatalf("definition JSON = %s", encoded)
	}
}

func TestNormalizeDefinitionRejectsDuplicatePortKeys(t *testing.T) {
	_, err := NormalizeDefinition(agentnode.Definition{
		Type:    "example.duplicate",
		Version: "1",
		Inputs: []agentnode.Port{
			{Key: "value"},
			{Key: "value"},
		},
	})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("error = %v, want ErrInvalidDefinition", err)
	}
}

func TestAdaptNormalizesResolvedPorts(t *testing.T) {
	node, err := Adapt(adapterFixtureNode{
		definition: validAdapterDefinition(),
		ports: agentnode.ResolvedPorts{
			Outputs: []agentnode.Port{{Key: "result"}, {Key: "result"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Resolve(json.RawMessage(`{}`))
	if !errors.Is(err, ErrInvalidResolvedPorts) {
		t.Fatalf("error = %v, want ErrInvalidResolvedPorts", err)
	}
}

func TestAdaptRejectsInvalidExecutionResults(t *testing.T) {
	tests := []struct {
		name   string
		result agentnode.Result
	}{
		{name: "unknown output", result: agentnode.Result{Outputs: map[string]any{"other": true}}},
		{name: "unknown active port", result: agentnode.Result{Outputs: map[string]any{"result": "ok"}, ActivePorts: []string{"other"}}},
		{name: "duplicate active port", result: agentnode.Result{Outputs: map[string]any{"result": "ok"}, ActivePorts: []string{"result", "result"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node, err := Adapt(adapterFixtureNode{
				definition: validAdapterDefinition(),
				ports: agentnode.ResolvedPorts{
					Outputs: []agentnode.Port{{Key: "result"}},
				},
				result: test.result,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
			if !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("error = %v, want ErrInvalidResult", err)
			}
		})
	}
}

func TestAdaptReturnsIndependentDefinitions(t *testing.T) {
	node, err := Adapt(adapterFixtureNode{definition: validAdapterDefinition()})
	if err != nil {
		t.Fatal(err)
	}
	first := node.Definition()
	first.Outputs[0].Key = "changed"
	second := node.Definition()
	if got, want := second.Outputs[0].Key, "result"; got != want {
		t.Fatalf("output key = %q, want %q", got, want)
	}
}

func validAdapterDefinition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "example.adapter",
		Version:      "1",
		ConfigSchema: agentnode.MustSchema(`{"type":"object"}`),
		Outputs: []agentnode.Port{{
			Key:         "result",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}
