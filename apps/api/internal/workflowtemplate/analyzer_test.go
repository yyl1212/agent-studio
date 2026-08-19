package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestAnalyzeBuildsPreviewFromExactRegisteredVersions(t *testing.T) {
	registry := newTemplateTestRegistry(t)
	analyzer := NewAnalyzer(engine.NewCompiler(registry), registry)

	analysis := analyzer.Analyze(validEchoTemplate())
	if !analysis.Preview.Valid || len(analysis.Preview.Issues) != 0 {
		t.Fatalf("preview=%+v", analysis.Preview)
	}
	if analysis.Preview.Summary.NodeCount != 3 || analysis.Preview.Summary.EdgeCount != 2 {
		t.Fatalf("summary=%+v", analysis.Preview.Summary)
	}
	if !bytes.Contains(analysis.Preview.Summary.InputSchema, []byte(`"topic"`)) {
		t.Fatalf("schema=%s", analysis.Preview.Summary.InputSchema)
	}
	echo := findSummary(t, analysis.Preview.Summary.NodeTypes, "extension.echo", "1.0.0")
	if !echo.Available || echo.Title != "Echo" || echo.Count != 1 {
		t.Fatalf("echo=%+v", echo)
	}

	missing := validEchoTemplate()
	missing.Spec.Graph.Nodes[1].TypeVersion = "9.9.9"
	invalid := analyzer.Analyze(missing)
	assertIssueCode(t, invalid.Preview.Issues, "NODE_TYPE_NOT_FOUND")
	if findSummary(t, invalid.Preview.Summary.NodeTypes, "extension.echo", "9.9.9").Available {
		t.Fatal("missing version marked available")
	}
	if invalid.Normalized.APIVersion != "" {
		t.Fatalf("invalid template was partially normalized: %+v", invalid.Normalized)
	}
}

func TestAnalyzeRejectsEnvelopeMetadataAndResourceLimits(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Template)
		wantCode string
	}{
		{name: "api version", mutate: func(template *Template) { template.APIVersion = "agent-studio.dev/v9" }, wantCode: "TEMPLATE_API_VERSION_UNSUPPORTED"},
		{name: "kind", mutate: func(template *Template) { template.Kind = "Other" }, wantCode: "TEMPLATE_KIND_INVALID"},
		{name: "empty name", mutate: func(template *Template) { template.Metadata.Name = "  " }, wantCode: "TEMPLATE_METADATA_INVALID"},
		{name: "long name", mutate: func(template *Template) { template.Metadata.Name = strings.Repeat("名", 129) }, wantCode: "TEMPLATE_METADATA_INVALID"},
		{name: "long description", mutate: func(template *Template) { template.Metadata.Description = strings.Repeat("说", 2049) }, wantCode: "TEMPLATE_METADATA_INVALID"},
		{name: "too many edges", mutate: func(template *Template) { template.Spec.Graph.Edges = make([]domain.Edge, MaxEdges+1) }, wantCode: "TEMPLATE_LIMIT_EXCEEDED"},
	}
	registry := newTemplateTestRegistry(t)
	analyzer := NewAnalyzer(engine.NewCompiler(registry), registry)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			template := validEchoTemplate()
			test.mutate(&template)
			analysis := analyzer.Analyze(template)
			if analysis.Preview.Valid {
				t.Fatalf("preview unexpectedly valid: %+v", analysis.Preview)
			}
			assertIssueCode(t, analysis.Preview.Issues, test.wantCode)
		})
	}
}

func TestAnalyzeStopsBeforeCompilerWhenNodeBudgetExceeded(t *testing.T) {
	compiler := &countingCompiler{}
	analyzer := NewAnalyzer(compiler, staticCatalog{})
	template := Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "超限"},
		Spec:       Spec{Graph: domain.Graph{SchemaVersion: 1, Nodes: make([]domain.Node, MaxNodes+1), Edges: []domain.Edge{}}},
	}
	analysis := analyzer.Analyze(template)
	assertIssueCode(t, analysis.Preview.Issues, "TEMPLATE_LIMIT_EXCEEDED")
	if compiler.calls != 0 {
		t.Fatalf("compiler called %d times", compiler.calls)
	}
}

func TestAnalyzeCompilerIssuesAreStable(t *testing.T) {
	registry := newTemplateTestRegistry(t)
	analyzer := NewAnalyzer(engine.NewCompiler(registry), registry)
	template := validEchoTemplate()
	template.Spec.Graph.Edges[0].SourcePort = "missing"
	template.Spec.Graph.Edges[1].Target = "missing-node"

	first := analyzer.Analyze(template)
	second := analyzer.Analyze(template)
	firstJSON, err := json.Marshal(first.Preview.Issues)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second.Preview.Issues)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("issue order drifted\n%s\n%s", firstJSON, secondJSON)
	}
	assertIssueCode(t, first.Preview.Issues, "SOURCE_PORT_NOT_FOUND")
	assertIssueCode(t, first.Preview.Issues, "EDGE_NODE_NOT_FOUND")
}

type nodeCase struct {
	name       string
	nodeType   string
	version    string
	startType  string
	inputPort  string
	outputPort string
	config     json.RawMessage
}

func TestAnalyzeOfficialNodeRoundTrip(t *testing.T) {
	registry := newTemplateTestRegistry(t)
	analyzer := NewAnalyzer(engine.NewCompiler(registry), registry)
	tests := []nodeCase{
		{name: "template", nodeType: "template", version: "1", startType: "text", inputPort: "topic", outputPort: "text", config: json.RawMessage(`{"template":"{{topic}}"}`)},
		{name: "condition", nodeType: "condition", version: "1", startType: "text", inputPort: "value", outputPort: "true", config: json.RawMessage(`{"operator":"isEmpty"}`)},
		{name: "llm", nodeType: "llm", version: "1", startType: "text", inputPort: "prompt", outputPort: "text", config: json.RawMessage(`{"model":"mock","maxTokens":32}`)},
		{name: "http", nodeType: "http", version: "1", startType: "json", inputPort: "body", outputPort: "body", config: json.RawMessage(`{"method":"POST","url":"https://example.com","headers":[],"timeoutMs":1000}`)},
		{name: "code", nodeType: "code", version: "1", startType: "json", inputPort: "input", outputPort: "result", config: json.RawMessage("{\"source\":\"def main(input):\\n  return input\"}")},
		{name: "echo", nodeType: "extension.echo", version: "1.0.0", startType: "text", inputPort: "text", outputPort: "text", config: json.RawMessage(`{"prefix":"回答："}`)},
		{name: "retriever", nodeType: "extension.retriever", version: "1.0.0", startType: "text", inputPort: "query", outputPort: "matches", config: json.RawMessage(`{"documents":[{"id":"doc-1","text":"Agent Studio"}],"topK":1}`)},
		{name: "webhook", nodeType: "extension.webhook", version: "1.0.0", startType: "json", inputPort: "body", outputPort: "body", config: json.RawMessage(`{"path":"hooks/run","timeoutMs":1000}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			first := analyzer.Analyze(singleNodeTemplate(test))
			if !first.Preview.Valid {
				t.Fatalf("issues=%+v", first.Preview.Issues)
			}
			firstBytes, err := Encode(first.Normalized)
			if err != nil {
				t.Fatal(err)
			}
			var decoded Template
			if err := json.Unmarshal(firstBytes, &decoded); err != nil {
				t.Fatal(err)
			}
			second := analyzer.Analyze(decoded)
			if !second.Preview.Valid {
				t.Fatalf("round-trip issues=%+v", second.Preview.Issues)
			}
			secondBytes, err := Encode(second.Normalized)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstBytes, secondBytes) {
				t.Fatalf("round trip drifted\n%s\n%s", firstBytes, secondBytes)
			}
		})
	}
}

func validEchoTemplate() Template {
	return Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: " Echo 模板 ", Description: "兼容性测试"},
		Spec: Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{
				{ID: "start", Type: "start", TypeVersion: "1", Config: json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`)},
				{ID: "echo", Type: "extension.echo", TypeVersion: "1.0.0", Config: json.RawMessage(`{"prefix":"回答："}`)},
				{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
			},
			Edges: []domain.Edge{
				{ID: "e1", Source: "start", SourcePort: "topic", Target: "echo", TargetPort: "text"},
				{ID: "e2", Source: "echo", SourcePort: "text", Target: "end", TargetPort: "result"},
			},
		}},
	}
}

func singleNodeTemplate(test nodeCase) Template {
	startKey := "input"
	if test.nodeType == "template" {
		startKey = "topic"
	}
	startConfig, _ := json.Marshal(map[string]any{"fields": []any{map[string]any{"key": startKey, "label": "输入", "type": test.startType, "required": true}}})
	return Template{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: test.name, Description: "往返"},
		Spec: Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{
				{ID: "start", Type: "start", TypeVersion: "1", Config: startConfig},
				{ID: "node", Type: test.nodeType, TypeVersion: test.version, Config: test.config},
				{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
			},
			Edges: []domain.Edge{
				{ID: "e1", Source: "start", SourcePort: startKey, Target: "node", TargetPort: test.inputPort},
				{ID: "e2", Source: "node", SourcePort: test.outputPort, Target: "end", TargetPort: "result"},
			},
		}},
	}
}

func newTemplateTestRegistry(t *testing.T) *nodes.Registry {
	t.Helper()
	registry := nodes.NewRegistry()
	if err := builtin.RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	if err := builtin.RegisterLLM(registry, modelprovider.NewMock(), "mock"); err != nil {
		t.Fatal(err)
	}
	if err := builtin.RegisterIntegrationNodes(registry, builtin.HTTPOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := generated.RegisterNodes(registry); err != nil {
		t.Fatal(err)
	}
	return registry
}

func findSummary(t *testing.T, summaries []NodeTypeSummary, nodeType, version string) NodeTypeSummary {
	t.Helper()
	for _, summary := range summaries {
		if summary.Type == nodeType && summary.Version == version {
			return summary
		}
	}
	t.Fatalf("summary %s@%s missing: %+v", nodeType, version, summaries)
	return NodeTypeSummary{}
}

func assertIssueCode(t *testing.T, issues []domain.ValidationIssue, code string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code == code {
			return
		}
	}
	t.Fatalf("issue %s missing: %+v", code, issues)
}

type countingCompiler struct {
	calls int
}

func (compiler *countingCompiler) Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue) {
	compiler.calls++
	return &engine.Plan{}, nil
}

type staticCatalog struct{}

func (staticCatalog) Definitions() []agentnode.Definition {
	return nil
}
