package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type fakeNode struct {
	kind    string
	version string
	schema  json.RawMessage
}

type publicSDKNode struct{}

func (publicSDKNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "example.public",
		Version:      "1.0.0",
		Title:        "Public",
		ConfigSchema: agentnode.MustSchema(`{"type":"object","additionalProperties":false}`),
		Outputs: []agentnode.Port{{
			Key:         "result",
			Title:       "Result",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}

func (publicSDKNode) Resolve(json.RawMessage) (agentnode.ResolvedPorts, error) {
	definition := publicSDKNode{}.Definition()
	return agentnode.ResolvedPorts{Outputs: definition.Outputs}, nil
}

func (publicSDKNode) Execute(context.Context, agentnode.Request) (agentnode.Result, error) {
	return agentnode.Result{Outputs: map[string]any{"result": "ok"}}, nil
}

func TestRegistryImplementsPublicRegistrar(t *testing.T) {
	registry := NewRegistry()
	var registrar agentnode.Registrar = registry
	if err := registrar.Register(publicSDKNode{}); err != nil {
		t.Fatal(err)
	}
	node, err := registry.Get("example.public", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Outputs["result"], "ok"; got != want {
		t.Fatalf("result = %v, want %v", got, want)
	}
}

func TestRegistryDefinitionsDoNotExposeMutablePorts(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(publicSDKNode{}); err != nil {
		t.Fatal(err)
	}
	first := registry.Definitions()
	first[0].Outputs[0].Key = "changed"
	second := registry.Definitions()
	if got, want := second[0].Outputs[0].Key, "result"; got != want {
		t.Fatalf("stored output key = %q, want %q", got, want)
	}
}

func (n fakeNode) Definition() domain.NodeDefinition {
	version := n.version
	if version == "" {
		version = "1"
	}
	schema := n.schema
	if schema == nil {
		schema = json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`)
	}
	return domain.NodeDefinition{
		Type:         n.kind,
		Version:      version,
		Title:        n.kind,
		ConfigSchema: schema,
	}
}

func (fakeNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	return domain.ResolvedPorts{}, nil
}

func (fakeNode) Execute(context.Context, domain.NodeRequest) (domain.NodeResult, error) {
	return domain.NodeResult{}, nil
}

func TestRegistryRegistersAndSortsDefinitions(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeNode{kind: "zeta"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeNode{kind: "alpha"}); err != nil {
		t.Fatal(err)
	}

	defs := r.Definitions()
	got := []string{defs[0].Type, defs[1].Type}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("definitions=%v, want %v", got, want)
	}
}

func TestRegistryRejectsDuplicateAndInvalidSchema(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeNode{kind: "echo"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(fakeNode{kind: "echo"}); !errors.Is(err, ErrDuplicateNodeType) {
		t.Fatalf("duplicate error=%v", err)
	}
	if err := r.Register(fakeNode{kind: "bad", schema: json.RawMessage(`{"type":`)}); err == nil {
		t.Fatal("expected invalid schema error")
	}
}

func TestRegistryValidatesConfigAndHandlesUnknownNode(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeNode{kind: "echo"}); err != nil {
		t.Fatal(err)
	}

	if err := r.ValidateConfig("echo", "1", json.RawMessage(`{"message":"hello"}`)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := r.ValidateConfig("echo", "1", json.RawMessage(`{"message":42}`)); err == nil {
		t.Fatal("expected schema validation error")
	}
	if _, err := r.Get("missing", "1"); !errors.Is(err, ErrNodeTypeNotFound) {
		t.Fatalf("unknown node error=%v", err)
	}
}
