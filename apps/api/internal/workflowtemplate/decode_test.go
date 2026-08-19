package workflowtemplate

import (
	"encoding/json"
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

func assertRequiredIssuePath(t *testing.T, issues []domain.ValidationIssue, wantPath string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == "TEMPLATE_FIELD_REQUIRED" && issue.Path == wantPath {
			return
		}
	}
	t.Fatalf("required issue for %q missing: %+v", wantPath, issues)
}
