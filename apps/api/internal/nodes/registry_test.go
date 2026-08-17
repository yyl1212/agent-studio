package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"agentstudio.local/api/internal/domain"
)

type fakeNode struct {
	kind    string
	version string
	schema  json.RawMessage
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
