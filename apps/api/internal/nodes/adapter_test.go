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
	resolveErr error
	executeErr error
}

type resolveTrackingNode struct {
	adapterFixtureNode
	resolveCalls int
}

func (node *resolveTrackingNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	node.resolveCalls++
	return node.ports, nil
}

func (node adapterFixtureNode) Definition() agentnode.Definition {
	return node.definition
}

func (node adapterFixtureNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	return node.ports, node.resolveErr
}

func (node adapterFixtureNode) Execute(context.Context, agentnode.Request) (agentnode.Result, error) {
	return node.result, node.executeErr
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

func TestNormalizeResolvedPortsUsesIndependentJSONArrays(t *testing.T) {
	ports, err := NormalizeResolvedPorts(agentnode.ResolvedPorts{})
	if err != nil {
		t.Fatal(err)
	}
	if ports.Inputs == nil || ports.Outputs == nil {
		t.Fatalf("ports must be non-nil: %#v", ports)
	}
	encoded, err := json.Marshal(ports)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"inputs":[],"outputs":[]}`; got != want {
		t.Fatalf("ports JSON = %s, want %s", got, want)
	}

	original := agentnode.ResolvedPorts{Outputs: []agentnode.Port{{Key: "result"}}}
	normalized, err := NormalizeResolvedPorts(original)
	if err != nil {
		t.Fatal(err)
	}
	normalized.Outputs[0].Key = "changed"
	if got, want := original.Outputs[0].Key, "result"; got != want {
		t.Fatalf("original output key = %q, want %q", got, want)
	}
}

func TestNormalizeResultRejectsInvalidExecutionResults(t *testing.T) {
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
			_, err := NormalizeResult(test.result, agentnode.ResolvedPorts{
				Outputs: []agentnode.Port{{Key: "result"}},
			})
			if !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("error = %v, want ErrInvalidResult", err)
			}
		})
	}
}

func TestAdaptExecuteDoesNotResolvePortsAgain(t *testing.T) {
	delegate := &resolveTrackingNode{adapterFixtureNode: adapterFixtureNode{
		definition: validAdapterDefinition(),
		ports: agentnode.ResolvedPorts{Outputs: []agentnode.Port{
			{Key: "result"},
			{Key: "result"},
		}},
		result: agentnode.Result{Outputs: map[string]any{"result": "ok"}},
	}}
	node, err := Adapt(delegate)
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if delegate.resolveCalls != 0 {
		t.Fatalf("Resolve calls = %d, want 0", delegate.resolveCalls)
	}
	if got, want := result.Outputs["result"], "ok"; got != want {
		t.Fatalf("result = %v, want %v", got, want)
	}
}

func TestNormalizeResultReturnsIndependentContainers(t *testing.T) {
	original := agentnode.Result{
		Outputs:     map[string]any{"result": "ok"},
		ActivePorts: []string{"result"},
	}
	normalized, err := NormalizeResult(original, agentnode.ResolvedPorts{
		Outputs: []agentnode.Port{{Key: "result"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	normalized.Outputs["result"] = "changed"
	normalized.ActivePorts[0] = "changed"
	if got, want := original.Outputs["result"], "ok"; got != want {
		t.Fatalf("original result = %v, want %v", got, want)
	}
	if got, want := original.ActivePorts[0], "result"; got != want {
		t.Fatalf("original active port = %q, want %q", got, want)
	}
}

func TestAdaptPropagatesDelegateErrors(t *testing.T) {
	resolveErr := errors.New("resolve failed")
	executeErr := errors.New("execute failed")
	node, err := Adapt(adapterFixtureNode{
		definition: validAdapterDefinition(),
		resolveErr: resolveErr,
		executeErr: executeErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Resolve(json.RawMessage(`{}`)); !errors.Is(err, resolveErr) {
		t.Fatalf("resolve error = %v, want %v", err, resolveErr)
	}
	if _, err := node.Execute(context.Background(), agentnode.Request{}); !errors.Is(err, executeErr) {
		t.Fatalf("execute error = %v, want %v", err, executeErr)
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
