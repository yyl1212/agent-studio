package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/engine"
	"agentstudio.local/api/internal/nodes"
	"agentstudio.local/api/internal/nodes/builtin"
)

type fakeStore struct {
	workflow   domain.Workflow
	versions   map[string]domain.WorkflowVersion
	currentID  string
	runs       []domain.Run
	nodeRuns   map[string]domain.NodeRun
	failUpsert error
	sequence   int
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	graph := graphReturning(t, "v1")
	return &fakeStore{
		workflow: domain.Workflow{
			ID:            "workflow-1",
			Name:          "演示 Agent",
			Slug:          "demo",
			Description:   "版本测试",
			DraftGraph:    graph,
			DraftRevision: 2,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		},
		versions: make(map[string]domain.WorkflowVersion),
		nodeRuns: make(map[string]domain.NodeRun),
	}
}

func (store *fakeStore) ListWorkflows(context.Context) ([]domain.Workflow, error) {
	return []domain.Workflow{store.workflow}, nil
}

func (store *fakeStore) CreateWorkflow(_ context.Context, workflow domain.Workflow) (domain.Workflow, error) {
	store.workflow = workflow
	return workflow, nil
}

func (store *fakeStore) GetWorkflow(_ context.Context, id string) (domain.Workflow, error) {
	if id != store.workflow.ID {
		return domain.Workflow{}, domain.ErrNotFound
	}
	return store.workflow, nil
}

func (store *fakeStore) UpdateDraft(_ context.Context, id string, revision int64, graph json.RawMessage) (domain.Workflow, error) {
	if id != store.workflow.ID {
		return domain.Workflow{}, domain.ErrNotFound
	}
	if revision != store.workflow.DraftRevision {
		return domain.Workflow{}, domain.ErrRevisionConflict
	}
	store.workflow.DraftGraph = append(json.RawMessage(nil), graph...)
	store.workflow.DraftRevision++
	return store.workflow, nil
}

func (store *fakeStore) Publish(_ context.Context, id string, revision int64, graph, inputSchema json.RawMessage) (domain.WorkflowVersion, error) {
	if id != store.workflow.ID || revision != store.workflow.DraftRevision {
		return domain.WorkflowVersion{}, domain.ErrRevisionConflict
	}
	version := store.AddVersion(graph, inputSchema)
	store.SetCurrentVersion(version)
	return version, nil
}

func (store *fakeStore) GetCurrentAgentVersion(_ context.Context, slug string) (domain.Workflow, domain.WorkflowVersion, error) {
	if slug != store.workflow.Slug || store.currentID == "" {
		return domain.Workflow{}, domain.WorkflowVersion{}, domain.ErrNotFound
	}
	return store.workflow, store.versions[store.currentID], nil
}

func (store *fakeStore) GetAgentVersion(_ context.Context, slug, versionID string) (domain.Workflow, domain.WorkflowVersion, error) {
	version, exists := store.versions[versionID]
	if slug != store.workflow.Slug || !exists || version.WorkflowID != store.workflow.ID {
		return domain.Workflow{}, domain.WorkflowVersion{}, domain.ErrNotFound
	}
	return store.workflow, version, nil
}

func (store *fakeStore) CreateRun(_ context.Context, run domain.Run) error {
	store.runs = append(store.runs, run)
	return nil
}

func (store *fakeStore) UpsertNodeRun(_ context.Context, nodeRun domain.NodeRun) error {
	if store.failUpsert != nil {
		return store.failUpsert
	}
	store.nodeRuns[nodeRun.NodeID] = nodeRun
	return nil
}

func (store *fakeStore) FinishRun(ctx context.Context, runID string, status domain.RunStatus, output any, publicError *domain.PublicError, endedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for index := range store.runs {
		if store.runs[index].ID == runID {
			store.runs[index].Status = status
			store.runs[index].Error = publicError
			store.runs[index].EndedAt = &endedAt
			encoded, _ := json.Marshal(output)
			store.runs[index].Output = encoded
			return nil
		}
	}
	return domain.ErrNotFound
}

func (store *fakeStore) GetRun(_ context.Context, runID string) (domain.Run, []domain.NodeRun, error) {
	for _, run := range store.runs {
		if run.ID == runID {
			nodeRuns := make([]domain.NodeRun, 0, len(store.nodeRuns))
			for _, nodeRun := range store.nodeRuns {
				nodeRuns = append(nodeRuns, nodeRun)
			}
			return run, nodeRuns, nil
		}
	}
	return domain.Run{}, nil, domain.ErrNotFound
}

func (store *fakeStore) ListRuns(context.Context, string, int) ([]domain.Run, error) {
	return append([]domain.Run(nil), store.runs...), nil
}

func (store *fakeStore) AddVersion(graph, inputSchema json.RawMessage) domain.WorkflowVersion {
	store.sequence++
	version := domain.WorkflowVersion{
		ID:          fmt.Sprintf("version-%d", store.sequence),
		WorkflowID:  store.workflow.ID,
		Version:     store.sequence,
		Graph:       append(json.RawMessage(nil), graph...),
		InputSchema: append(json.RawMessage(nil), inputSchema...),
		CreatedAt:   time.Now().UTC(),
	}
	store.versions[version.ID] = version
	return version
}

func (store *fakeStore) SetCurrentVersion(version domain.WorkflowVersion) {
	store.currentID = version.ID
	store.workflow.PublishedVersionID = &version.ID
	store.workflow.PublishedVersion = &version.Version
}

func (store *fakeStore) LastRun() domain.Run {
	return store.runs[len(store.runs)-1]
}

func TestPublishRequiresExactRevision(t *testing.T) {
	service, store := newServiceFixture(t)
	workflow := store.workflow
	_, err := service.Publish(context.Background(), workflow.ID, workflow.DraftRevision-1)
	if !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestCreateValidatesIdentityAndAllowsIncompleteInitialDraft(t *testing.T) {
	service, _ := newServiceFixture(t)
	if _, err := service.Create(context.Background(), CreateWorkflowInput{Name: "", Slug: "demo"}); !errors.Is(err, ErrInvalidWorkflowInput) {
		t.Fatalf("empty name error=%v", err)
	}
	if _, err := service.Create(context.Background(), CreateWorkflowInput{Name: "Demo", Slug: "Bad Slug"}); !errors.Is(err, ErrInvalidWorkflowInput) {
		t.Fatalf("invalid slug error=%v", err)
	}
	created, err := service.Create(context.Background(), CreateWorkflowInput{Name: "新 Agent", Slug: "new-agent", Description: "草稿"})
	if err != nil {
		t.Fatal(err)
	}
	var graph domain.Graph
	if err := json.Unmarshal(created.DraftGraph, &graph); err != nil {
		t.Fatal(err)
	}
	if created.DraftRevision != 1 || len(graph.Nodes) != 2 || len(graph.Edges) != 0 {
		t.Fatalf("created=%+v graph=%+v", created, graph)
	}
}

func TestPublishValidatesGraphAndBuildsAgentManifest(t *testing.T) {
	service, store := newServiceFixture(t)
	version, err := service.Publish(context.Background(), store.workflow.ID, store.workflow.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := service.AgentManifest(context.Background(), store.workflow.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.WorkflowVersionID != version.ID || manifest.Title != store.workflow.Name || manifest.Version != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}

	store.workflow.DraftGraph = json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`)
	_, err = service.Publish(context.Background(), store.workflow.ID, store.workflow.DraftRevision)
	var validationError *ValidationError
	if !errors.As(err, &validationError) || len(validationError.Issues) == 0 {
		t.Fatalf("validation error=%v", err)
	}
}

func newServiceFixture(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore(t)
	return NewService(store, newRealCompiler(t)), store
}

func newRealCompiler(t *testing.T) *engine.Compiler {
	t.Helper()
	registry := nodes.NewRegistry()
	if err := builtin.RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	return engine.NewCompiler(registry)
}

func graphReturning(t *testing.T, prefix string) json.RawMessage {
	return graphReturningField(t, prefix, "topic")
}

func graphReturningField(t *testing.T, prefix, field string) json.RawMessage {
	t.Helper()
	startConfig, err := json.Marshal(map[string]any{"fields": []any{map[string]any{"key": field, "label": "输入", "type": "text", "required": true}}})
	if err != nil {
		t.Fatal(err)
	}
	templateConfig, err := json.Marshal(map[string]any{"template": prefix + "{{" + field + "}}"})
	if err != nil {
		t.Fatal(err)
	}
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			{ID: "start", Type: "start", TypeVersion: "1", Config: startConfig},
			{ID: "template", Type: "template", TypeVersion: "1", Config: templateConfig},
			{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
		},
		Edges: []domain.Edge{
			{ID: "e1", Source: "start", SourcePort: field, Target: "template", TargetPort: field},
			{ID: "e2", Source: "template", SourcePort: "text", Target: "end", TargetPort: "result"},
		},
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
