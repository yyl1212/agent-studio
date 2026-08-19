package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestEncodeCanonicalTemplate(t *testing.T) {
	input := Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "演示", Description: "确定性"},
		Spec: Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{
				{ID: "z", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
				{ID: "a", Type: "start", TypeVersion: "1", Config: json.RawMessage(`{"z":1,"a":{"d":4,"b":2}}`)},
			},
			Edges: nil,
		}},
	}
	original := append(json.RawMessage(nil), input.Spec.Graph.Nodes[1].Config...)

	first, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("encoding drifted\n%s\n%s", first, second)
	}
	if !bytes.Equal(input.Spec.Graph.Nodes[1].Config, original) {
		t.Fatal("input config was mutated")
	}
	if bytes.Index(first, []byte(`"id": "a"`)) > bytes.Index(first, []byte(`"id": "z"`)) {
		t.Fatalf("nodes not sorted: %s", first)
	}
	if !bytes.Contains(first, []byte(`"edges": []`)) || !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatalf("arrays/newline not canonical: %s", first)
	}
	if bytes.Index(first, []byte(`"b": 2`)) > bytes.Index(first, []byte(`"d": 4`)) {
		t.Fatalf("config keys not sorted: %s", first)
	}
}

func TestCanonicalizeRejectsInvalidConfigJSON(t *testing.T) {
	input := Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Spec: Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{
				{ID: "broken", Type: "start", TypeVersion: "1", Config: json.RawMessage(`{"broken":`)},
			},
		}},
	}

	normalized, err := Canonicalize(input)
	if err == nil {
		t.Fatal("expected invalid config JSON to be rejected")
	}
	if !reflect.DeepEqual(normalized, Template{}) {
		t.Fatalf("partial template returned: %+v", normalized)
	}
}

func TestEncodeCanonicalTemplatePreservesLargeIntegers(t *testing.T) {
	input := Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "大整数"},
		Spec: Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{{
				ID: "number", Type: "custom", TypeVersion: "1",
				Config: json.RawMessage(`{"value":9007199254740993}`),
			}},
			Edges: []domain.Edge{},
		}},
	}

	encoded, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`9007199254740993`)) {
		t.Fatalf("large integer changed during canonicalization: %s", encoded)
	}
}

func TestEncodeV1Alpha2SortsPackageHintsAndRoundTrips(t *testing.T) {
	input := Template{
		APIVersion: APIVersionV1Alpha2,
		Kind:       Kind,
		Metadata:   Metadata{Name: "包提示"},
		Spec: Spec{
			NodePackages: []NodePackageRequirement{
				{Name: "example.com/zeta", Nodes: []NodePackageNode{{Type: "zeta.b", Version: "2"}, {Type: "zeta.a", Version: "1"}}},
				{Name: "example.com/alpha", Version: "v1.0.0", Nodes: []NodePackageNode{{Type: "alpha.node", Version: "1"}}},
			},
			Graph: domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{}, Edges: []domain.Edge{}},
		},
	}
	first, err := Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(first, []byte(`example.com/alpha`)) > bytes.Index(first, []byte(`example.com/zeta`)) ||
		bytes.Index(first, []byte(`zeta.a`)) > bytes.Index(first, []byte(`zeta.b`)) {
		t.Fatalf("package hints not sorted: %s", first)
	}
	decoded, err := Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("round trip drifted\n%s\n%s", first, second)
	}
}
