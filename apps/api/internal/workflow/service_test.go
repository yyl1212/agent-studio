package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/generated"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
)

type fakeStore struct {
	workflow    domain.Workflow
	versions    map[string]domain.WorkflowVersion
	currentID   string
	runs        []domain.Run
	nodeRuns    map[string]domain.NodeRun
	failUpsert  error
	sequence    int
	createCalls int
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
	store.createCalls++
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

func TestImportTemplateCreatesNewUnpublishedDraft(t *testing.T) {
	service, store := newServiceFixture(t)
	input := ImportWorkflowTemplateInput{
		Template: marshalTemplateFixture(t, validEchoTemplateFixture(t)),
		Name:     "导入副本", Slug: "imported-copy", Description: "本地模板",
	}
	created, err := service.ImportTemplate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if store.createCalls != 1 || created.DraftRevision != 1 || created.PublishedVersionID != nil || created.PublishedVersion != nil {
		t.Fatalf("created=%+v calls=%d", created, store.createCalls)
	}
	if created.ID == "workflow-1" || created.Slug != "imported-copy" {
		t.Fatalf("created=%+v", created)
	}
}

func TestInvalidTemplateDoesNotWrite(t *testing.T) {
	service, store := newServiceFixture(t)
	template := validEchoTemplateFixture(t)
	template.Spec.Graph.Nodes[1].TypeVersion = "9.9.9"
	input := ImportWorkflowTemplateInput{Template: marshalTemplateFixture(t, template), Name: "副本", Slug: "copy"}
	_, err := service.ImportTemplate(context.Background(), input)
	var validation *TemplateValidationError
	if !errors.As(err, &validation) || store.createCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, store.createCalls)
	}
}

func TestImportTemplateRejectsInvalidIdentityWithoutWriting(t *testing.T) {
	service, store := newServiceFixture(t)
	_, err := service.ImportTemplate(context.Background(), ImportWorkflowTemplateInput{
		Template: marshalTemplateFixture(t, validEchoTemplateFixture(t)), Name: "副本", Slug: "Bad Slug",
	})
	if !errors.Is(err, ErrInvalidWorkflowInput) || store.createCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, store.createCalls)
	}
}

func TestPreviewTemplateDoesNotWrite(t *testing.T) {
	service, store := newServiceFixture(t)
	preview, err := service.PreviewTemplate(context.Background(), marshalTemplateFixture(t, validEchoTemplateFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Valid || store.createCalls != 0 {
		t.Fatalf("preview=%+v calls=%d", preview, store.createCalls)
	}
}

func TestTemplateDecodeErrorsDoNotWrite(t *testing.T) {
	service, store := newServiceFixture(t)
	invalid := json.RawMessage(`{"apiVersion":"agent-studio.dev/v1alpha1","unknown":true}`)
	if _, err := service.PreviewTemplate(context.Background(), invalid); err == nil {
		t.Fatal("expected preview decode error")
	}
	_, err := service.ImportTemplate(context.Background(), ImportWorkflowTemplateInput{
		Template: invalid, Name: "副本", Slug: "decode-error",
	})
	if err == nil || store.createCalls != 0 {
		t.Fatalf("err=%v calls=%d", err, store.createCalls)
	}
}

func TestExportTemplateRequiresExactRevisionBeforeGraphValidation(t *testing.T) {
	service, store := newServiceFixture(t)
	store.workflow.DraftGraph = json.RawMessage(`{"broken":`)
	_, err := service.ExportTemplate(context.Background(), store.workflow.ID, store.workflow.DraftRevision-1)
	if !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestExportTemplateUsesStoredIdentityWithoutDatabaseFields(t *testing.T) {
	service, store := newServiceFixture(t)
	exported, err := service.ExportTemplate(context.Background(), store.workflow.ID, store.workflow.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Filename != "demo.workflow.json" {
		t.Fatalf("filename=%q", exported.Filename)
	}
	if bytes.Contains(exported.Data, []byte(store.workflow.ID)) || bytes.Contains(exported.Data, []byte(`"draftRevision"`)) {
		t.Fatalf("database fields leaked: %s", exported.Data)
	}
}

func TestTemplateExportImportExportRoundTripIsStable(t *testing.T) {
	service, store := newServiceFixture(t)
	first, err := service.ExportTemplate(context.Background(), store.workflow.ID, store.workflow.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.ImportTemplate(context.Background(), ImportWorkflowTemplateInput{
		Template: first.Data, Name: store.workflow.Name, Slug: "demo-copy", Description: store.workflow.Description,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ExportTemplate(context.Background(), created.ID, created.DraftRevision)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Data, second.Data) {
		t.Fatalf("round trip drifted\n%s\n%s", first.Data, second.Data)
	}
}

func validEchoTemplateFixture(t *testing.T) workflowtemplate.Template {
	t.Helper()
	return workflowtemplate.Template{
		APIVersion: workflowtemplate.APIVersion,
		Kind:       workflowtemplate.Kind,
		Metadata:   workflowtemplate.Metadata{Name: "Echo 模板", Description: "Service 测试"},
		Spec: workflowtemplate.Spec{Graph: domain.Graph{
			SchemaVersion: 1,
			Nodes: []domain.Node{
				{ID: "start", Type: "start", TypeVersion: "1", Config: json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text"}]}`)},
				{ID: "echo", Type: "extension.echo", TypeVersion: "1.0.0", Config: json.RawMessage(`{"prefix":""}`)},
				{ID: "end", Type: "end", TypeVersion: "1", Config: json.RawMessage(`{}`)},
			},
			Edges: []domain.Edge{
				{ID: "e1", Source: "start", SourcePort: "topic", Target: "echo", TargetPort: "text"},
				{ID: "e2", Source: "echo", SourcePort: "text", Target: "end", TargetPort: "result"},
			},
		}},
	}
}

func marshalTemplateFixture(t *testing.T, template workflowtemplate.Template) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(template)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func newServiceFixture(t *testing.T) (*Service, *fakeStore) {
	t.Helper()
	store := newFakeStore(t)
	registry := nodes.NewRegistry()
	if err := builtin.RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	if err := generated.RegisterNodes(registry); err != nil {
		t.Fatal(err)
	}
	compiler := engine.NewCompiler(registry)
	return NewService(store, compiler, registry), store
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
