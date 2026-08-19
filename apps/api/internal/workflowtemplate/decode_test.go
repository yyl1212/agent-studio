package workflowtemplate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

const requiredTemplateJSON = `{
  "apiVersion":"agent-studio.dev/v1alpha1",
  "kind":"WorkflowTemplate",
  "metadata":{"name":"demo","description":""},
  "spec":{"graph":{"schemaVersion":1,"nodes":[
    {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}},
    {"id":"end","type":"end","typeVersion":"1","position":{"x":0,"y":0},"config":{}}
  ],"edges":[
    {"id":"edge","source":"start","sourcePort":"topic","target":"end","targetPort":"result"}
  ]}}
}`

func TestDecodeRecordsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		path string
	}{
		{
			name: "metadata description",
			raw:  strings.Replace(requiredTemplateJSON, `,"description":""`, "", 1),
			path: "metadata.description",
		},
		{
			name: "spec graph",
			raw: `{
  "apiVersion":"agent-studio.dev/v1alpha1",
  "kind":"WorkflowTemplate",
  "metadata":{"name":"demo","description":""},
  "spec":{}
}`,
			path: "spec.graph",
		},
		{
			name: "node position",
			raw:  strings.Replace(requiredTemplateJSON, `,"position":{"x":0,"y":0}`, "", 1),
			path: "spec.graph.nodes[0].position",
		},
		{
			name: "position y",
			raw:  strings.Replace(requiredTemplateJSON, `"position":{"x":0,"y":0}`, `"position":{"x":0}`, 1),
			path: "spec.graph.nodes[0].position.y",
		},
		{
			name: "node config",
			raw: strings.Replace(
				requiredTemplateJSON,
				`,"config":{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`,
				"",
				1,
			),
			path: "spec.graph.nodes[0].config",
		},
		{
			name: "edge target port",
			raw:  strings.Replace(requiredTemplateJSON, `,"targetPort":"result"`, "", 1),
			path: "spec.graph.edges[0].targetPort",
		},
	}

	registry := newTemplateTestRegistry(t)
	analyzer := NewAnalyzer(engine.NewCompiler(registry), registry)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := Decode(json.RawMessage(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			analysis := analyzer.Analyze(decoded)
			if analysis.Preview.Valid {
				t.Fatalf("preview unexpectedly valid: %+v", analysis.Preview)
			}
			assertRequiredIssuePath(t, analysis.Preview.Issues, test.path)
		})
	}
}

func TestDecodeAcceptsPresentZeroValues(t *testing.T) {
	decoded, err := Decode(json.RawMessage(requiredTemplateJSON))
	if err != nil {
		t.Fatal(err)
	}
	registry := newTemplateTestRegistry(t)
	analysis := NewAnalyzer(engine.NewCompiler(registry), registry).Analyze(decoded)
	for _, issue := range analysis.Preview.Issues {
		if issue.Code == "TEMPLATE_FIELD_REQUIRED" {
			t.Fatalf("present zero value reported missing: %+v", issue)
		}
	}
	if !analysis.Preview.Valid {
		t.Fatalf("preview=%+v", analysis.Preview)
	}
}

func TestDecodeMigratesV1Alpha1ToLatest(t *testing.T) {
	decoded, err := Decode(json.RawMessage(requiredTemplateJSON))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.APIVersion != APIVersionV1Alpha2 || decoded.Spec.NodePackages == nil || len(decoded.Spec.NodePackages) != 0 {
		t.Fatalf("decoded=%+v", decoded)
	}
	encoded, err := Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"apiVersion": "agent-studio.dev/v1alpha2"`) ||
		!strings.Contains(string(encoded), `"nodePackages": []`) {
		t.Fatalf("encoded=%s", encoded)
	}
}

func TestDecodeV1Alpha2ValidatesPackageHints(t *testing.T) {
	valid := `{
  "apiVersion":"agent-studio.dev/v1alpha2",
  "kind":"WorkflowTemplate",
  "metadata":{"name":"demo","description":""},
  "spec":{"nodePackages":[{"name":"example.com/nodes","version":"v1.2.3","nodes":[{"type":"example.search","version":"1.0.0"}]}],"graph":{"schemaVersion":1,"nodes":[],"edges":[]}}
}`
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown dependency field", raw: strings.Replace(valid, `"version":"v1.2.3"`, `"version":"v1.2.3","repository":"https://example.com"`, 1)},
		{name: "null dependency list", raw: strings.Replace(valid, `"nodePackages":[{"name":"example.com/nodes","version":"v1.2.3","nodes":[{"type":"example.search","version":"1.0.0"}]}]`, `"nodePackages":null`, 1)},
		{name: "duplicate package", raw: strings.Replace(valid,
			`[{"name":"example.com/nodes","version":"v1.2.3","nodes":[{"type":"example.search","version":"1.0.0"}]}]`,
			`[{"name":"example.com/nodes","version":"v1.2.3","nodes":[{"type":"example.search","version":"1.0.0"}]},{"name":"example.com/nodes","nodes":[{"type":"example.other","version":"1"}]}]`, 1)},
		{name: "duplicate node", raw: strings.Replace(valid, `{"type":"example.search","version":"1.0.0"}`, `{"type":"example.search","version":"1.0.0"},{"type":"example.search","version":"1.0.0"}`, 1)},
		{name: "invalid package version", raw: strings.Replace(valid, `"v1.2.3"`, `"1.2.3"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(json.RawMessage(test.raw)); err == nil {
				t.Fatal("expected package hint to be rejected")
			}
		})
	}

	omitted := strings.Replace(valid,
		`"nodePackages":[{"name":"example.com/nodes","version":"v1.2.3","nodes":[{"type":"example.search","version":"1.0.0"}]}],`, "", 1)
	decoded, err := Decode(json.RawMessage(omitted))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Spec.NodePackages == nil || len(decoded.Spec.NodePackages) != 0 {
		t.Fatalf("nodePackages=%+v", decoded.Spec.NodePackages)
	}
	unsupported, err := Decode(json.RawMessage(strings.Replace(valid, APIVersionV1Alpha2, "agent-studio.dev/v9", 1)))
	if err != nil || unsupported.APIVersion != "agent-studio.dev/v9" {
		t.Fatalf("unsupported version should reach analyzer: template=%+v err=%v", unsupported, err)
	}
}

func TestDecodeV1Alpha2EnforcesPackageHintBudgets(t *testing.T) {
	base := Template{
		APIVersion: APIVersionV1Alpha2,
		Kind:       Kind,
		Metadata:   Metadata{Name: "budget", Description: ""},
		Spec: Spec{NodePackages: []NodePackageRequirement{}, Graph: domain.Graph{
			SchemaVersion: 1, Nodes: []domain.Node{}, Edges: []domain.Edge{},
		}},
	}
	tooManyPackages := base
	for index := 0; index < MaxNodePackages+1; index++ {
		tooManyPackages.Spec.NodePackages = append(tooManyPackages.Spec.NodePackages, NodePackageRequirement{
			Name:  fmt.Sprintf("example.com/package-%d", index),
			Nodes: []NodePackageNode{{Type: fmt.Sprintf("example.node.%d", index), Version: "1"}},
		})
	}
	tooManyNodes := base
	tooManyNodes.Spec.NodePackages = []NodePackageRequirement{{Name: "example.com/nodes", Nodes: []NodePackageNode{}}}
	for index := 0; index < MaxPackageNodes+1; index++ {
		tooManyNodes.Spec.NodePackages[0].Nodes = append(tooManyNodes.Spec.NodePackages[0].Nodes,
			NodePackageNode{Type: fmt.Sprintf("example.node.%d", index), Version: "1"})
	}
	for _, test := range []struct {
		name     string
		template Template
	}{
		{name: "packages", template: tooManyPackages},
		{name: "nodes", template: tooManyNodes},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.template)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(raw); err == nil {
				t.Fatal("expected package hint budget error")
			}
		})
	}
}

func assertRequiredIssuePath(t *testing.T, issues []domain.ValidationIssue, wantPath string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == "TEMPLATE_FIELD_REQUIRED" && issue.Path == wantPath {
			return
		}
	}
	t.Fatalf("required issue for %q missing: %+v", wantPath, issues)
}
