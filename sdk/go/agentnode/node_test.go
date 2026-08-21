package agentnode_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type echoNode struct{}

func (echoNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "example.echo",
		Version:      "1.0.0",
		Title:        "Echo",
		Description:  "Echoes text",
		Category:     "Examples",
		ConfigSchema: agentnode.MustSchema(`{"type":"object","additionalProperties":false}`),
		Inputs: []agentnode.Port{{
			Key:         "text",
			Title:       "Text",
			Type:        agentnode.DataTypeString,
			Required:    true,
			Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: []agentnode.Port{{
			Key:         "text",
			Title:       "Text",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}

func (echoNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	definition := echoNode{}.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (echoNode) Execute(_ context.Context, request agentnode.Request) (agentnode.Result, error) {
	return agentnode.Result{Outputs: map[string]any{"text": request.Inputs["text"][0]}}, nil
}

func TestNodeCanBeImplementedOutsideRuntime(t *testing.T) {
	var node agentnode.Node = echoNode{}
	result, err := node.Execute(context.Background(), agentnode.Request{
		Inputs: map[string][]any{"text": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Outputs["text"], "hello"; got != want {
		t.Fatalf("text = %v, want %v", got, want)
	}
}

func TestDefinitionKeepsRuntimeJSONShape(t *testing.T) {
	encoded, err := json.Marshal(echoNode{}.Definition())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"type", "version", "title", "description", "category",
		"configSchema", "inputs", "outputs",
	}
	for _, key := range wantKeys {
		if _, exists := got[key]; !exists {
			t.Errorf("definition JSON missing %q: %s", key, encoded)
		}
	}
	inputs, ok := got["inputs"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatalf("inputs = %#v", got["inputs"])
	}
	port, ok := inputs[0].(map[string]any)
	if !ok {
		t.Fatalf("port = %#v", inputs[0])
	}
	wantPort := map[string]any{
		"key": "text", "title": "Text", "type": "string",
		"required": true, "cardinality": "one",
	}
	if !reflect.DeepEqual(port, wantPort) {
		t.Fatalf("port = %#v, want %#v", port, wantPort)
	}
}

func TestPublicEnumValuesMatchExistingProtocol(t *testing.T) {
	dataTypes := []agentnode.DataType{
		agentnode.DataTypeString,
		agentnode.DataTypeNumber,
		agentnode.DataTypeBoolean,
		agentnode.DataTypeJSON,
		agentnode.DataTypeAny,
	}
	if got, want := dataTypes, []agentnode.DataType{"string", "number", "boolean", "json", "any"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("data types = %#v, want %#v", got, want)
	}
	cardinalities := []agentnode.Cardinality{
		agentnode.CardinalityOne,
		agentnode.CardinalitySingleActive,
	}
	if got, want := cardinalities, []agentnode.Cardinality{"one", "single-active"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("cardinalities = %#v, want %#v", got, want)
	}
}

func TestSupportsOnlyCurrentAPIVersion(t *testing.T) {
	if !agentnode.SupportsAPIVersion("agent-studio.dev/v1alpha1") {
		t.Fatal("current API version must be supported")
	}
	if agentnode.SupportsAPIVersion("agent-studio.dev/v1alpha2") {
		t.Fatal("unknown API version must be rejected")
	}
}

func TestVersionForV030(t *testing.T) {
	if got, want := agentnode.Version, "0.3.0"; got != want {
		t.Fatalf("Version = %q, want %q", got, want)
	}
}
