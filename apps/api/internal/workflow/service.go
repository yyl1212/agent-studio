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
)

var (
	ErrInvalidWorkflowInput = errors.New("invalid workflow input")
	slugPattern             = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
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

type AgentManifest struct {
	WorkflowVersionID string          `json:"workflowVersionId"`
	Version           int             `json:"version"`
	Title             string          `json:"title"`
	Description       string          `json:"description"`
	InputSchema       json.RawMessage `json:"inputSchema"`
}

type Service struct {
	store    Store
	compiler Compiler
}

func NewService(store Store, compiler Compiler) *Service {
	return &Service{store: store, compiler: compiler}
}

func (service *Service) List(ctx context.Context) ([]domain.Workflow, error) {
	return service.store.ListWorkflows(ctx)
}

func (service *Service) Get(ctx context.Context, id string) (domain.Workflow, error) {
	return service.store.GetWorkflow(ctx, id)
}

func (service *Service) Create(ctx context.Context, input CreateWorkflowInput) (domain.Workflow, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" || !slugPattern.MatchString(input.Slug) {
		return domain.Workflow{}, ErrInvalidWorkflowInput
	}
	graph := domain.Graph{
		SchemaVersion: 1,
		Nodes: []domain.Node{
			{ID: uuid.NewString(), Type: "start", TypeVersion: "1", Position: domain.Position{X: 120, Y: 180}, Config: json.RawMessage(`{"fields":[]}`)},
			{ID: uuid.NewString(), Type: "end", TypeVersion: "1", Position: domain.Position{X: 520, Y: 180}, Config: json.RawMessage(`{}`)},
		},
		Edges: []domain.Edge{},
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
