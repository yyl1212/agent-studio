package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestSemanticDiffOmitsSecretValuesFromBothSides(t *testing.T) {
	definitions := []agentnode.Definition{{
		Type: "secure", Version: "1", Title: "安全节点",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "token":{"type":"string","writeOnly":true},
            "credential":{"type":"object","writeOnly":true},
            "label":{"type":"string"}
          }
        }`),
	}}
	base := diffSnapshotWithNodeConfig(t, "secure", "1", `{"token":"before-secret","credential":{"value":"before-parent"},"label":"old"}`)
	compare := diffSnapshotWithNodeConfig(t, "secure", "1", `{"token":"after-secret","credential":{"value":"after-parent"},"label":"new"}`)
	got, err := newSemanticDiffEngine(definitions).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	for _, secret := range []string{"before-secret", "after-secret", "before-parent", "after-parent"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("secret leaked: %s", encoded)
		}
	}
	if len(got.Groups.Nodes) != 1 || len(got.Groups.Nodes[0].Config) != 3 {
		t.Fatalf("nodes=%+v", got.Groups.Nodes)
	}
	for _, path := range []string{"/config/credential/value", "/config/token"} {
		change := findConfigChange(t, got.Groups.Nodes[0].Config, path)
		if change.ValueOmitted == nil || *change.ValueOmitted != domain.WorkflowDiffSecret || change.Before != nil || change.After != nil {
			t.Fatalf("secret diff=%+v", change)
		}
	}
}

func TestSemanticDiffFailsClosedWhenDefinitionUnavailable(t *testing.T) {
	base := diffSnapshotWithNodeConfig(t, "missing", "1", `{"value":"before"}`)
	compare := diffSnapshotWithNodeConfig(t, "missing", "1", `{"value":"after"}`)
	got, err := newSemanticDiffEngine(nil).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Nodes != 1 || len(got.Groups.Nodes) != 1 || len(got.Groups.Nodes[0].Config) != 1 {
		t.Fatalf("diff=%+v", got)
	}
	change := got.Groups.Nodes[0].Config[0]
	if change.Path != "/config" || change.ValueOmitted == nil || *change.ValueOmitted != domain.WorkflowDiffDefinitionUnavailable || change.Before != nil || change.After != nil {
		t.Fatalf("change=%+v", change)
	}
}

func TestSemanticDiffBudgetsDetailsAndLargeValues(t *testing.T) {
	baseConfig := make(map[string]any, 502)
	compareConfig := make(map[string]any, 502)
	baseConfig["a_large"], compareConfig["a_large"] = strings.Repeat("a", 4097), strings.Repeat("b", 4097)
	for index := 0; index < 501; index++ {
		key := fmt.Sprintf("f%03d", index)
		baseConfig[key], compareConfig[key] = index, index+1
	}
	base := diffSnapshotWithConfigValue(t, baseConfig)
	compare := diffSnapshotWithConfigValue(t, compareConfig)
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Nodes != 502 || got.Summary.Total != 502 || !got.Truncated || len(got.Groups.Nodes) != 1 || len(got.Groups.Nodes[0].Config) != 500 {
		t.Fatalf("summary=%+v truncated=%v details=%d", got.Summary, got.Truncated, len(got.Groups.Nodes[0].Config))
	}
	large := findConfigChange(t, got.Groups.Nodes[0].Config, "/config/a_large")
	if large.ValueOmitted == nil || *large.ValueOmitted != domain.WorkflowDiffTooLarge || large.Before != nil || large.After != nil {
		t.Fatalf("large=%+v", large)
	}
}

func TestVersionGovernanceDiffLoadsExactSnapshots(t *testing.T) {
	graph := snapshotGraph("text", "null")
	store := snapshotFixtureStore(graph, snapshotTextInputSchema())
	revision, version := store.workflow.DraftRevision, 1
	service := NewVersionGovernanceService(store, nil, emptyVersionCatalog{})
	got, err := service.Diff(context.Background(), store.workflow.ID, WorkflowDiffRequest{
		Base:    domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotVersion, Version: &version},
		Compare: domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotDraft, DraftRevision: &revision},
	})
	if err != nil || got.Summary.Total != 0 || got.Base.Version == nil || got.Compare.DraftRevision == nil {
		t.Fatalf("diff=%+v err=%v", got, err)
	}

	stale := revision - 1
	_, err = service.Diff(context.Background(), store.workflow.ID, WorkflowDiffRequest{
		Base:    domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotDraft, DraftRevision: &stale},
		Compare: domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotVersion, Version: &version},
	})
	if !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("stale draft err=%v", err)
	}
}

func TestSemanticDiffGroupsWorkflowChangesDeterministically(t *testing.T) {
	base := mustDiffSnapshot(t, `{
      "schemaVersion":1,
      "nodes":[
        {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"topic","label":"主题","type":"text"}]}},
        {"id":"template","type":"template","typeVersion":"1","position":{"x":100,"y":0},"config":{"template":"{{topic}}"}}
      ],
      "edges":[{"id":"old-id","source":"start","sourcePort":"topic","target":"template","targetPort":"topic"}]
    }`)
	compare := mustDiffSnapshot(t, `{
      "schemaVersion":1,
      "nodes":[
        {"id":"template","type":"template","typeVersion":"1","position":{"x":100.2,"y":0},"config":{"template":"主题：{{topic}}"}},
        {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"topic","label":"问题","type":"textarea"},{"key":"tone","label":"语气","type":"select","options":["brief"]}]}}
      ],
      "edges":[{"id":"new-id","source":"start","sourcePort":"topic","target":"template","targetPort":"topic"}]
    }`)
	compare.Presentation.SubmitLabel = "开始分析"
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Nodes != 1 || got.Summary.StartParameters != 3 || got.Summary.Connections != 0 || got.Summary.AgentPresentation != 1 || got.Summary.Layout != 1 || got.Summary.Total != 6 {
		t.Fatalf("summary=%+v", got.Summary)
	}
	if len(got.Groups.Nodes) != 1 || got.Groups.Nodes[0].NodeID != "template" || got.Groups.Nodes[0].Config[0].Path != "/config/template" {
		t.Fatalf("nodes=%+v", got.Groups.Nodes)
	}
	if len(got.Groups.StartParameters) != 2 || got.Groups.StartParameters[0].Key != "tone" || got.Groups.StartParameters[1].Key != "topic" {
		t.Fatalf("start parameters=%+v", got.Groups.StartParameters)
	}
	if len(got.Groups.Layout) != 1 || got.Groups.Layout[0].NodeID != "template" {
		t.Fatalf("layout=%+v", got.Groups.Layout)
	}
}

func TestSemanticDiffNormalizesObjectsNumbersAndNodeOrder(t *testing.T) {
	base := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[
      {"id":"b","type":"template","typeVersion":"1","position":{"x":0,"y":0},"config":{"object":{"a":1,"b":2},"number":1}},
      {"id":"a","type":"end","typeVersion":"1","position":{"x":1,"y":1},"config":{}}
    ],"edges":[]}`)
	compare := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[
      {"id":"a","type":"end","typeVersion":"1","position":{"x":1.04,"y":1.04},"config":{}},
      {"id":"b","type":"template","typeVersion":"1","position":{"x":0,"y":0},"config":{"number":1.0,"object":{"b":2.0,"a":1.0}}}
    ],"edges":[]}`)
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Total != 0 || got.Truncated {
		t.Fatalf("diff=%+v", got)
	}
}

func TestSemanticDiffTreatsArraysAndMissingNullAsChanges(t *testing.T) {
	base := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[{"id":"n","type":"template","typeVersion":"1","position":{"x":0,"y":0},"config":{"array":[1,2]}}],"edges":[]}`)
	compare := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[{"id":"n","type":"template","typeVersion":"1","position":{"x":0,"y":0},"config":{"array":[2,1],"missing":null}}],"edges":[]}`)
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Nodes != 3 || len(got.Groups.Nodes) != 1 || len(got.Groups.Nodes[0].Config) != 3 {
		t.Fatalf("diff=%+v", got)
	}
	missing := got.Groups.Nodes[0].Config[2]
	if missing.Path != "/config/missing" || missing.Before != nil || missing.After == nil || string(*missing.After) != "null" {
		t.Fatalf("missing/null diff=%+v", missing)
	}
}

func TestSemanticDiffUsesConnectionMultisetAndIgnoresEdgeIDs(t *testing.T) {
	base := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[],"edges":[
      {"id":"one","source":"a","sourcePort":"out","target":"b","targetPort":"in"},
      {"id":"two","source":"a","sourcePort":"out","target":"b","targetPort":"in"}
    ]}`)
	compare := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[],"edges":[
      {"id":"changed","source":"a","sourcePort":"out","target":"b","targetPort":"in"}
    ]}`)
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Connections != 1 || len(got.Groups.Connections) != 1 || got.Groups.Connections[0].Kind != domain.WorkflowDiffRemoved {
		t.Fatalf("connections=%+v summary=%+v", got.Groups.Connections, got.Summary)
	}
}

func TestSemanticDiffReportsEachMovedStartFieldAndQuantizedLayout(t *testing.T) {
	base := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[
      {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"a","label":"A","type":"text"},{"key":"b","label":"B","type":"text"}]}},
      {"id":"n","type":"end","typeVersion":"1","position":{"x":10,"y":10},"config":{}}
    ],"edges":[]}`)
	compare := mustDiffSnapshot(t, `{"schemaVersion":1,"nodes":[
      {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"b","label":"B","type":"text"},{"key":"a","label":"A","type":"text"}]}},
      {"id":"n","type":"end","typeVersion":"1","position":{"x":10.06,"y":10.04},"config":{}}
    ],"edges":[]}`)
	got, err := newSemanticDiffEngine(diffDefinitions()).Diff(base, compare)
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.StartParameters != 2 || len(got.Groups.StartParameters) != 2 || got.Summary.Layout != 1 {
		t.Fatalf("diff=%+v", got)
	}
	for _, field := range got.Groups.StartParameters {
		if field.Kind != domain.WorkflowDiffReordered || field.BeforeOrder == nil || field.AfterOrder == nil {
			t.Fatalf("field=%+v", field)
		}
	}
}

func mustDiffSnapshot(t *testing.T, raw string) workflowSnapshot {
	t.Helper()
	var graph domain.Graph
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		t.Fatal(err)
	}
	return workflowSnapshot{
		Descriptor: domain.WorkflowSnapshotDescriptor{Kind: domain.WorkflowSnapshotDraft},
		Graph:      graph,
		Presentation: domain.AgentPresentation{
			Title: "Agent", Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto,
		},
	}
}

func diffDefinitions() []agentnode.Definition {
	return []agentnode.Definition{
		{Type: "start", Version: "1", Title: "开始", ConfigSchema: json.RawMessage(`{"type":"object"}`)},
		{Type: "template", Version: "1", Title: "模板", ConfigSchema: json.RawMessage(`{"type":"object"}`)},
		{Type: "end", Version: "1", Title: "结束", ConfigSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func diffSnapshotWithNodeConfig(t *testing.T, nodeType, version, config string) workflowSnapshot {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(config), &value); err != nil {
		t.Fatal(err)
	}
	return diffSnapshotWithNode(t, nodeType, version, value)
}

func diffSnapshotWithConfigValue(t *testing.T, config any) workflowSnapshot {
	t.Helper()
	return diffSnapshotWithNode(t, "template", "1", config)
}

func diffSnapshotWithNode(t *testing.T, nodeType, version string, config any) workflowSnapshot {
	t.Helper()
	rawConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	graph := domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{{
		ID: "n", Type: nodeType, TypeVersion: version, Position: domain.Position{}, Config: rawConfig,
	}}, Edges: []domain.Edge{}}
	return workflowSnapshot{
		Descriptor: domain.WorkflowSnapshotDescriptor{Kind: domain.WorkflowSnapshotDraft},
		Graph:      graph,
		Presentation: domain.AgentPresentation{
			Title: "Agent", Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto,
		},
	}
}

func findConfigChange(t *testing.T, changes []domain.WorkflowValueDiff, path string) domain.WorkflowValueDiff {
	t.Helper()
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("config change %s not found in %+v", path, changes)
	return domain.WorkflowValueDiff{}
}
