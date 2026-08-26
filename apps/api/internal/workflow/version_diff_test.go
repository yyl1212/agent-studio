package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

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
