package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflowtemplate"
)

var (
	ErrInvalidWorkflowInput    = errors.New("invalid workflow input")
	ErrInvalidWorkflowTemplate = errors.New("invalid workflow template")
	slugPattern                = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type ValidationError struct {
	Issues []domain.ValidationIssue
}

func (err *ValidationError) Error() string {
	return "工作流校验失败"
}

type CreateWorkflowInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type TemplateExport struct {
	Filename string
	Data     []byte
}

type ImportWorkflowTemplateInput struct {
	Template    json.RawMessage `json:"template"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description string          `json:"description"`
}

type TemplateValidationError struct {
	Issues []domain.ValidationIssue
}

func (err *TemplateValidationError) Error() string {
	return "工作流模板校验失败"
}

type AgentManifest struct {
	WorkflowVersionID string          `json:"workflowVersionId"`
	Version           int             `json:"version"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	InputSchema       json.RawMessage `json:"inputSchema"`
}

type Service struct {
	store     Store
	compiler  Compiler
	templates *workflowtemplate.Analyzer
}

func NewService(store Store, compiler Compiler, catalog workflowtemplate.NodeCatalog) *Service {
	return &Service{store: store, compiler: compiler, templates: workflowtemplate.NewAnalyzer(compiler, catalog)}
}

func (service *Service) List(ctx context.Context) ([]domain.Workflow, error) {
	return service.store.ListWorkflows(ctx)
}

func (service *Service) Get(ctx context.Context, id string) (domain.Workflow, error) {
	return service.store.GetWorkflow(ctx, id)
}

func (service *Service) Create(ctx context.Context, input CreateWorkflowInput) (domain.Workflow, error) {
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			{ID: uuid.NewString(), Type: "start", TypeVersion: "1", Position: domain.Position{X: 120, Y: 180}, Config: json.RawMessage(`{"fields":[]}`)},
			{ID: uuid.NewString(), Type: "end", TypeVersion: "1", Position: domain.Position{X: 520, Y: 180}, Config: json.RawMessage(`{}`)},
		},
		Edges: []domain.Edge{},
	}
	return service.createWithGraph(ctx, input, graph)
}

func (service *Service) createWithGraph(ctx context.Context, input CreateWorkflowInput, graph domain.Graph) (domain.Workflow, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" || !slugPattern.MatchString(input.Slug) {
		return domain.Workflow{}, ErrInvalidWorkflowInput
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("encode initial graph: %w", err)
	}
	workflow, err := service.store.CreateWorkflow(ctx, domain.Workflow{
		ID:            uuid.NewString(),
		Name:          input.Name,
		Slug:          input.Slug,
		Description:   input.Description,
		DraftGraph:    raw,
		DraftRevision: 1,
	})
	if errors.Is(err, domain.ErrSlugConflict) {
		return domain.Workflow{}, domain.ErrSlugConflict
	}
	return workflow, err
}

func (service *Service) ExportTemplate(ctx context.Context, id string, revision int64) (TemplateExport, error) {
	loaded, err := service.store.GetWorkflow(ctx, id)
	if err != nil {
		return TemplateExport{}, err
	}
	if loaded.DraftRevision != revision {
		return TemplateExport{}, domain.ErrRevisionConflict
	}
	var graph domain.Graph
	if err := json.Unmarshal(loaded.DraftGraph, &graph); err != nil {
		return TemplateExport{}, &TemplateValidationError{Issues: []domain.ValidationIssue{{
			Code: "GRAPH_JSON_INVALID", Message: "工作流图 JSON 无效", Path: "spec.graph",
		}}}
	}
	analysis := service.templates.Analyze(workflowtemplate.Template{
		APIVersion: workflowtemplate.APIVersion,
		Kind:       workflowtemplate.Kind,
		Metadata:   workflowtemplate.Metadata{Name: loaded.Name, Description: loaded.Description},
		Spec:       workflowtemplate.Spec{Graph: graph},
	})
	if !analysis.Preview.Valid {
		return TemplateExport{}, &TemplateValidationError{Issues: analysis.Preview.Issues}
	}
	data, err := workflowtemplate.Encode(analysis.Normalized)
	if err != nil {
		return TemplateExport{}, fmt.Errorf("encode workflow template: %w", err)
	}
	return TemplateExport{Filename: loaded.Slug + ".workflow.json", Data: data}, nil
}

func (service *Service) PreviewTemplate(_ context.Context, raw json.RawMessage) (workflowtemplate.Preview, error) {
	template, err := decodeWorkflowTemplate(raw)
	if err != nil {
		return workflowtemplate.Preview{}, err
	}
	return service.templates.Analyze(template).Preview, nil
}

func (service *Service) ImportTemplate(ctx context.Context, input ImportWorkflowTemplateInput) (domain.Workflow, error) {
	template, err := decodeWorkflowTemplate(input.Template)
	if err != nil {
		return domain.Workflow{}, err
	}
	analysis := service.templates.Analyze(template)
	if !analysis.Preview.Valid {
		return domain.Workflow{}, &TemplateValidationError{Issues: analysis.Preview.Issues}
	}
	return service.createWithGraph(ctx, CreateWorkflowInput{
		Name: input.Name, Slug: input.Slug, Description: input.Description,
	}, analysis.Normalized.Spec.Graph)
}

func decodeWorkflowTemplate(raw json.RawMessage) (workflowtemplate.Template, error) {
	template, err := workflowtemplate.Decode(raw)
	if err != nil {
		return workflowtemplate.Template{}, fmt.Errorf("%w: %v", ErrInvalidWorkflowTemplate, err)
	}
	return template, nil
}

func (service *Service) SaveDraft(ctx context.Context, id string, revision int64, graph domain.Graph) (domain.Workflow, error) {
	if graph.SchemaVersion != 1 {
		return domain.Workflow{}, ErrInvalidWorkflowInput
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("encode draft graph: %w", err)
	}
	return service.store.UpdateDraft(ctx, id, revision, raw)
}

func (service *Service) Validate(ctx context.Context, id string) []domain.ValidationIssue {
	workflow, err := service.store.GetWorkflow(ctx, id)
	if err != nil {
		return []domain.ValidationIssue{{Code: "WORKFLOW_LOAD_FAILED", Message: "无法加载工作流"}}
	}
	graph, issues := decodeAndCompile(service.compiler, workflow.DraftGraph)
	_ = graph
	return issues
}

func (service *Service) Publish(ctx context.Context, id string, revision int64) (domain.WorkflowVersion, error) {
	workflow, err := service.store.GetWorkflow(ctx, id)
	if err != nil {
		return domain.WorkflowVersion{}, err
	}
	if workflow.DraftRevision != revision {
		return domain.WorkflowVersion{}, domain.ErrRevisionConflict
	}
	graph, issues := decodeAndCompile(service.compiler, workflow.DraftGraph)
	if len(issues) > 0 {
		return domain.WorkflowVersion{}, &ValidationError{Issues: issues}
	}
	inputSchema, err := deriveInputSchema(graph)
	if err != nil {
		return domain.WorkflowVersion{}, err
	}
	graphJSON, err := json.Marshal(graph)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("encode published graph: %w", err)
	}
	return service.store.Publish(ctx, id, revision, graphJSON, inputSchema)
}

func (service *Service) AgentManifest(ctx context.Context, slug string) (AgentManifest, error) {
	workflow, version, err := service.store.GetCurrentAgentVersion(ctx, slug)
	if err != nil {
		return AgentManifest{}, err
	}
	return AgentManifest{
		WorkflowVersionID: version.ID,
		Version:           version.Version,
		Title:             workflow.Name,
		Description:       workflow.Description,
		InputSchema:       version.InputSchema,
	}, nil
}

func decodeAndCompile(compiler Compiler, raw json.RawMessage) (domain.Graph, []domain.ValidationIssue) {
	var graph domain.Graph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return domain.Graph{}, []domain.ValidationIssue{{Code: "GRAPH_JSON_INVALID", Message: "工作流图 JSON 无效", Path: "graph"}}
	}
	_, issues := compiler.Compile(graph)
	return graph, issues
}

func deriveInputSchema(graph domain.Graph) (json.RawMessage, error) {
	for _, node := range graph.Nodes {
		if node.Type == "start" {
			return builtin.DeriveInputSchema(node.Config)
		}
	}
	return nil, fmt.Errorf("start node missing")
}
