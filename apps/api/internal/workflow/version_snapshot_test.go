package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestLoadDraftSnapshotRequiresExactRevision(t *testing.T) {
	store := &versionGovernanceFixtureStore{workflow: snapshotFixtureWorkflow(8, snapshotGraph("text", "null"))}
	service := NewVersionGovernanceService(store, nil, emptyVersionCatalog{})
	revision := int64(7)
	_, err := service.loadSnapshot(context.Background(), store.workflow.ID,
		domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotDraft, DraftRevision: &revision})
	if !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadVersionSnapshotRejectsInputSchemaDrift(t *testing.T) {
	store := snapshotFixtureStore(snapshotGraph("text", "null"), json.RawMessage(`{"type":"object","properties":{"other":{"type":"string"}}}`))
	version := 1
	_, err := NewVersionGovernanceService(store, nil, emptyVersionCatalog{}).loadSnapshot(
		context.Background(), store.workflow.ID,
		domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotVersion, Version: &version})
	if !errors.Is(err, domain.ErrWorkflowSnapshotUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadVersionSnapshotAcceptsCanonicalNumericSchemaAndBuildsDescriptor(t *testing.T) {
	graph := snapshotGraph("number", "1")
	schema := json.RawMessage(`{
      "$schema":"https://json-schema.org/draft/2020-12/schema",
      "type":"object",
      "properties":{"topic":{"title":"主题","type":"number","default":1.0}},
      "additionalProperties":false,
      "x-ui-order":["topic"],
      "required":["topic"]
    }`)
	store := snapshotFixtureStore(graph, schema)
	version := 1
	got, err := NewVersionGovernanceService(store, nil, emptyVersionCatalog{}).loadSnapshot(
		context.Background(), store.workflow.ID,
		domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotVersion, Version: &version})
	if err != nil {
		t.Fatal(err)
	}
	if got.Descriptor.Kind != domain.WorkflowSnapshotVersion || got.Descriptor.Version == nil || *got.Descriptor.Version != 1 ||
		got.Descriptor.VersionID == nil || *got.Descriptor.VersionID != store.version.ID || got.Descriptor.CreatedAt == nil || !got.Descriptor.CreatedAt.Equal(store.version.CreatedAt) {
		t.Fatalf("descriptor=%+v", got.Descriptor)
	}
}

func TestLoadSnapshotRejectsInvalidReferencesBeforeStore(t *testing.T) {
	store := snapshotFixtureStore(snapshotGraph("text", "null"), snapshotTextInputSchema())
	version, revision := 1, int64(8)
	refs := []domain.WorkflowSnapshotRef{
		{Kind: domain.WorkflowSnapshotDraft},
		{Kind: domain.WorkflowSnapshotDraft, Version: &version, DraftRevision: &revision},
		{Kind: domain.WorkflowSnapshotVersion},
		{Kind: domain.WorkflowSnapshotVersion, Version: &version, DraftRevision: &revision},
		{Kind: "unknown"},
	}
	for _, ref := range refs {
		if _, err := NewVersionGovernanceService(store, nil, emptyVersionCatalog{}).loadSnapshot(context.Background(), store.workflow.ID, ref); !errors.Is(err, ErrInvalidWorkflowInput) {
			t.Fatalf("ref=%+v err=%v", ref, err)
		}
	}
}

func TestLoadSnapshotRejectsUnsafeHistoricalGraphs(t *testing.T) {
	deepConfig := strings.Repeat(`{"child":`, 64) + `null` + strings.Repeat(`}`, 64)
	tooLargeConfig := `{"value":"` + strings.Repeat("x", (2<<20)+1) + `"}`
	tests := []struct {
		name  string
		graph json.RawMessage
	}{
		{name: "invalid json", graph: json.RawMessage(`{"schemaVersion":1`)},
		{name: "schema version", graph: json.RawMessage(`{"schemaVersion":2,"nodes":[],"edges":[]}`)},
		{name: "duplicate node id", graph: json.RawMessage(`{"schemaVersion":1,"nodes":[{"id":"n","type":"x","typeVersion":"1","position":{"x":0,"y":0},"config":{}},{"id":"n","type":"x","typeVersion":"1","position":{"x":0,"y":0},"config":{}}],"edges":[]}`)},
		{name: "duplicate edge id", graph: json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[{"id":"e","source":"a","sourcePort":"out","target":"b","targetPort":"in"},{"id":"e","source":"a","sourcePort":"out","target":"b","targetPort":"in"}]}`)},
		{name: "config depth", graph: graphWithRawConfig(deepConfig)},
		{name: "graph bytes", graph: graphWithRawConfig(tooLargeConfig)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflow := snapshotFixtureWorkflow(8, test.graph)
			store := &versionGovernanceFixtureStore{workflow: workflow}
			revision := workflow.DraftRevision
			_, err := NewVersionGovernanceService(store, nil, emptyVersionCatalog{}).loadSnapshot(
				context.Background(), workflow.ID,
				domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotDraft, DraftRevision: &revision})
			if !errors.Is(err, domain.ErrWorkflowSnapshotUnsupported) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestLoadVersionSnapshotPreservesVersionNotFound(t *testing.T) {
	store := &versionGovernanceFixtureStore{workflow: snapshotFixtureWorkflow(8, snapshotGraph("text", "null"))}
	version := 99
	_, err := NewVersionGovernanceService(store, nil, emptyVersionCatalog{}).loadSnapshot(
		context.Background(), store.workflow.ID,
		domain.WorkflowSnapshotRef{Kind: domain.WorkflowSnapshotVersion, Version: &version})
	if !errors.Is(err, domain.ErrWorkflowVersionNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func snapshotFixtureStore(graph, inputSchema json.RawMessage) *versionGovernanceFixtureStore {
	createdAt := time.Date(2026, 8, 27, 1, 0, 0, 0, time.UTC)
	workflow := snapshotFixtureWorkflow(8, graph)
	return &versionGovernanceFixtureStore{
		workflow: workflow,
		version: domain.WorkflowVersion{
			ID: "22222222-2222-4222-8222-222222222222", WorkflowID: workflow.ID, Version: 1,
			Graph: graph, InputSchema: inputSchema, AgentPresentation: workflow.AgentPresentation, CreatedAt: createdAt,
		},
	}
}

func snapshotFixtureWorkflow(revision int64, graph json.RawMessage) domain.Workflow {
	return domain.Workflow{
		ID: versionGovernanceWorkflowID, DraftRevision: revision, DraftGraph: graph,
		AgentPresentation: domain.AgentPresentation{
			Title: "Agent", Accent: domain.AgentAccentIndigo, SubmitLabel: "运行", ResultMode: domain.AgentResultModeAuto,
		},
	}
}

func snapshotGraph(fieldType, defaultValue string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
      "schemaVersion":1,
      "nodes":[
        {"id":"start","type":"start","typeVersion":"1","position":{"x":0,"y":0},"config":{"fields":[{"key":"topic","label":"主题","type":%q,"required":true,"default":%s}]}},
        {"id":"end","type":"end","typeVersion":"1","position":{"x":100,"y":0},"config":{}}
      ],
      "edges":[]
    }`, fieldType, defaultValue))
}

func snapshotTextInputSchema() json.RawMessage {
	return json.RawMessage(`{
      "$schema":"https://json-schema.org/draft/2020-12/schema",
      "type":"object",
      "properties":{"topic":{"title":"主题","type":"string","x-ui-widget":"text"}},
      "additionalProperties":false,
      "x-ui-order":["topic"],
      "required":["topic"]
    }`)
}

func graphWithRawConfig(config string) json.RawMessage {
	return json.RawMessage(`{"schemaVersion":1,"nodes":[{"id":"n","type":"missing","typeVersion":"1","position":{"x":0,"y":0},"config":` + config + `}],"edges":[]}`)
}
