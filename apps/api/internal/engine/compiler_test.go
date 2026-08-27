package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type compilerFixtureNode struct {
	definition domain.NodeDefinition
}

func (node compilerFixtureNode) Definition() domain.NodeDefinition {
	return node.definition
}

func (node compilerFixtureNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	return domain.ResolvedPorts{Inputs: node.definition.Inputs, Outputs: node.definition.Outputs}, nil
}

func (compilerFixtureNode) Execute(context.Context, domain.NodeRequest) (domain.NodeResult, error) {
	return domain.NodeResult{}, nil
}

func TestCompilerRejectsCycleAndMultipleEnds(t *testing.T) {
	graph := graphFixture(
		[]domain.Node{
			nodeFixture("start", "start"),
			nodeFixture("first", "pass"),
			nodeFixture("second", "pass"),
			nodeFixture("end-a", "end"),
			nodeFixture("end-b", "end"),
		},
		[]domain.Edge{
			edgeFixture("e1", "start", "out", "first", "in"),
			edgeFixture("e2", "first", "out", "second", "in"),
			edgeFixture("e3", "second", "out", "first", "in"),
			edgeFixture("e4", "second", "out", "end-a", "result"),
			edgeFixture("e5", "second", "out", "end-b", "result"),
		},
	)
	_, issues := newFixtureCompiler(t).Compile(graph)
	assertIssueCodes(t, issues, "WORKFLOW_CYCLE", "WORKFLOW_END_COUNT")
}

func TestCompilerAllowsMultipleEdgesOnlyForSingleActive(t *testing.T) {
	valid := graphFixture(
		[]domain.Node{
			nodeFixture("start", "start"),
			nodeFixture("condition", "condition"),
			nodeFixture("yes", "pass"),
			nodeFixture("no", "pass"),
			nodeFixture("end", "end"),
		},
		[]domain.Edge{
			edgeFixture("e1", "start", "out", "condition", "value"),
			edgeFixture("e2", "condition", "true", "yes", "in"),
			edgeFixture("e3", "condition", "false", "no", "in"),
			edgeFixture("e4", "yes", "out", "end", "result"),
			edgeFixture("e5", "no", "out", "end", "result"),
		},
	)
	plan, issues := newFixtureCompiler(t).Compile(valid)
	if len(issues) != 0 {
		t.Fatalf("issues=%+v", issues)
	}
	if plan.StartNodeID != "start" || plan.EndNodeID != "end" || len(plan.TopologicalOrder) != 5 {
		t.Fatalf("plan=%+v", plan)
	}

	invalid := graphFixture(
		[]domain.Node{nodeFixture("start", "start"), nodeFixture("pass", "pass"), nodeFixture("end", "end")},
		[]domain.Edge{
			edgeFixture("e1", "start", "out", "pass", "in"),
			edgeFixture("e2", "start", "out", "pass", "in"),
			edgeFixture("e3", "pass", "out", "end", "result"),
		},
	)
	_, issues = newFixtureCompiler(t).Compile(invalid)
	assertIssueCodes(t, issues, "PORT_CARDINALITY_VIOLATION")
}

func TestCompilerRejectsDisconnectedRequiredPort(t *testing.T) {
	graph := graphFixture(
		[]domain.Node{nodeFixture("start", "start"), nodeFixture("join", "two-input"), nodeFixture("end", "end")},
		[]domain.Edge{
			edgeFixture("e1", "start", "out", "join", "left"),
			edgeFixture("e2", "join", "out", "end", "result"),
		},
	)
	_, issues := newFixtureCompiler(t).Compile(graph)
	assertIssueCodes(t, issues, "PORT_REQUIRED_CONNECTION_MISSING")
}

func TestCompilerReportsNodeEdgeTypeAndReachabilityErrors(t *testing.T) {
	tests := []struct {
		name  string
		graph domain.Graph
		codes []string
	}{
		{
			name: "unknown node version",
			graph: graphFixture(
				[]domain.Node{{ID: "start", Type: "start", TypeVersion: "99", Config: json.RawMessage(`{}`)}, nodeFixture("end", "end")},
				[]domain.Edge{edgeFixture("e1", "start", "out", "end", "result")},
			),
			codes: []string{"NODE_TYPE_NOT_FOUND"},
		},
		{
			name: "invalid config",
			graph: graphFixture(
				[]domain.Node{nodeFixture("start", "start"), nodeFixture("configured", "configured"), nodeFixture("end", "end")},
				[]domain.Edge{edgeFixture("e1", "start", "out", "configured", "in"), edgeFixture("e2", "configured", "out", "end", "result")},
			),
			codes: []string{"NODE_CONFIG_INVALID"},
		},
		{
			name: "dangling and self edges",
			graph: graphFixture(
				[]domain.Node{nodeFixture("start", "start"), nodeFixture("pass", "pass"), nodeFixture("end", "end")},
				[]domain.Edge{
					edgeFixture("e1", "start", "out", "missing", "in"),
					edgeFixture("e2", "pass", "out", "pass", "in"),
					edgeFixture("e3", "pass", "out", "end", "result"),
				},
			),
			codes: []string{"EDGE_NODE_NOT_FOUND", "EDGE_SELF_LOOP"},
		},
		{
			name: "type mismatch",
			graph: graphFixture(
				[]domain.Node{nodeFixture("start", "start"), nodeFixture("number", "number-pass"), nodeFixture("end", "end")},
				[]domain.Edge{edgeFixture("e1", "start", "out", "number", "in"), edgeFixture("e2", "number", "out", "end", "result")},
			),
			codes: []string{"PORT_TYPE_MISMATCH"},
		},
		{
			name: "unreachable and no end path",
			graph: graphFixture(
				[]domain.Node{nodeFixture("start", "start"), nodeFixture("dead", "pass"), nodeFixture("end", "end")},
				[]domain.Edge{edgeFixture("e1", "start", "out", "dead", "in")},
			),
			codes: []string{"NODE_UNREACHABLE_FROM_START", "NODE_CANNOT_REACH_END"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, issues := newFixtureCompiler(t).Compile(test.graph)
			assertIssueCodes(t, issues, test.codes...)
			if !sort.SliceIsSorted(issues, func(i, j int) bool { return issueSortKey(issues[i]) < issueSortKey(issues[j]) }) {
				t.Fatalf("issues are not deterministic: %+v", issues)
			}
		})
	}
}

func newFixtureCompiler(t *testing.T) *Compiler {
	t.Helper()
	registry := nodes.NewRegistry()
	definitions := []domain.NodeDefinition{
		{Type: "start", Version: "1", Title: "Start", ConfigSchema: objectSchema(), Outputs: []domain.PortDefinition{port("out", domain.TypeString, false, domain.CardinalityOne)}},
		{Type: "pass", Version: "1", Title: "Pass", ConfigSchema: objectSchema(), Inputs: []domain.PortDefinition{port("in", domain.TypeString, true, domain.CardinalityOne)}, Outputs: []domain.PortDefinition{port("out", domain.TypeString, false, domain.CardinalityOne)}, ExecutionSafety: agentnode.ExecutionSafetyReadOnly},
		{Type: "number-pass", Version: "1", Title: "Number", ConfigSchema: objectSchema(), Inputs: []domain.PortDefinition{port("in", domain.TypeNumber, true, domain.CardinalityOne)}, Outputs: []domain.PortDefinition{port("out", domain.TypeNumber, false, domain.CardinalityOne)}},
		{Type: "two-input", Version: "1", Title: "Join", ConfigSchema: objectSchema(), Inputs: []domain.PortDefinition{port("left", domain.TypeString, true, domain.CardinalityOne), port("right", domain.TypeString, true, domain.CardinalityOne)}, Outputs: []domain.PortDefinition{port("out", domain.TypeString, false, domain.CardinalityOne)}},
		{Type: "condition", Version: "1", Title: "Condition", ConfigSchema: objectSchema(), Inputs: []domain.PortDefinition{port("value", domain.TypeString, true, domain.CardinalityOne)}, Outputs: []domain.PortDefinition{port("true", domain.TypeString, false, domain.CardinalityOne), port("false", domain.TypeString, false, domain.CardinalityOne)}},
		{Type: "end", Version: "1", Title: "End", ConfigSchema: objectSchema(), Inputs: []domain.PortDefinition{port("result", domain.TypeAny, true, domain.CardinalitySingleActive)}},
		{Type: "configured", Version: "1", Title: "Configured", ConfigSchema: json.RawMessage(`{"type":"object","properties":{"enabled":{"type":"boolean"}},"required":["enabled"],"additionalProperties":false}`), Inputs: []domain.PortDefinition{port("in", domain.TypeString, true, domain.CardinalityOne)}, Outputs: []domain.PortDefinition{port("out", domain.TypeString, false, domain.CardinalityOne)}},
	}
	for _, definition := range definitions {
		if err := registry.Register(compilerFixtureNode{definition: definition}); err != nil {
			t.Fatal(err)
		}
	}
	return NewCompiler(registry)
}

func graphFixture(graphNodes []domain.Node, edges []domain.Edge) domain.Graph {
	return domain.Graph{SchemaVersion: 1, Nodes: graphNodes, Edges: edges}
}

func nodeFixture(id, nodeType string) domain.Node {
	return domain.Node{ID: id, Type: nodeType, TypeVersion: "1", Config: json.RawMessage(`{}`)}
}

func edgeFixture(id, source, sourcePort, target, targetPort string) domain.Edge {
	return domain.Edge{ID: id, Source: source, SourcePort: sourcePort, Target: target, TargetPort: targetPort}
}

func port(key string, dataType domain.DataType, required bool, cardinality domain.PortCardinality) domain.PortDefinition {
	return domain.PortDefinition{Key: key, Title: key, Type: dataType, Required: required, Cardinality: cardinality}
}

func objectSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func assertIssueCodes(t *testing.T, issues []domain.ValidationIssue, expected ...string) {
	t.Helper()
	actual := make(map[string]bool, len(issues))
	for _, issue := range issues {
		actual[issue.Code] = true
	}
	for _, code := range expected {
		if !actual[code] {
			t.Fatalf("missing issue %s in %+v", code, issues)
		}
	}
}

func issueSortKey(issue domain.ValidationIssue) string {
	return issue.NodeID + "\x00" + issue.EdgeID + "\x00" + issue.Path + "\x00" + issue.Code
}

func TestCompilerProducesStableTopologicalOrder(t *testing.T) {
	graph := graphFixture(
		[]domain.Node{nodeFixture("start", "start"), nodeFixture("z", "pass"), nodeFixture("a", "pass"), nodeFixture("end", "end")},
		[]domain.Edge{
			edgeFixture("e1", "start", "out", "z", "in"),
			edgeFixture("e2", "start", "out", "a", "in"),
			edgeFixture("e3", "z", "out", "end", "result"),
			edgeFixture("e4", "a", "out", "end", "result"),
		},
	)
	plan, issues := newFixtureCompiler(t).Compile(graph)
	if len(issues) != 0 {
		t.Fatalf("issues=%+v", issues)
	}
	if want := []string{"start", "a", "z", "end"}; !reflect.DeepEqual(plan.TopologicalOrder, want) {
		t.Fatalf("order=%v, want %v", plan.TopologicalOrder, want)
	}
}

func TestCompilerCachesNormalizedExecutionSafety(t *testing.T) {
	plan, issues := newFixtureCompiler(t).Compile(graphFixture(
		[]domain.Node{nodeFixture("start", "start"), nodeFixture("pass", "pass"), nodeFixture("end", "end")},
		[]domain.Edge{edgeFixture("e1", "start", "out", "pass", "in"), edgeFixture("e2", "pass", "out", "end", "result")},
	))
	if len(issues) != 0 {
		t.Fatalf("issues=%+v", issues)
	}
	if plan.Nodes["pass"].ExecutionSafety != agentnode.ExecutionSafetyReadOnly {
		t.Fatalf("execution safety=%q", plan.Nodes["pass"].ExecutionSafety)
	}
}
