package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const workflowSelectColumns = `w.id::text,w.name,w.slug,w.description,w.agent_presentation,w.draft_graph,w.draft_revision,
    w.published_version_id::text,pv.version,w.archived_at,w.created_at,w.updated_at`

func (store *Store) ListWorkflows(ctx context.Context) ([]domain.Workflow, error) {
	rows, err := store.pool.Query(ctx, `SELECT `+workflowSelectColumns+`
        FROM workflows w
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.archived_at IS NULL
        ORDER BY w.updated_at DESC,w.id`)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()
	workflows := make([]domain.Workflow, 0)
	for rows.Next() {
		workflow, err := scanWorkflow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow: %w", err)
		}
		workflows = append(workflows, workflow)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	return workflows, nil
}

func (store *Store) CreateWorkflow(ctx context.Context, workflow domain.Workflow) (domain.Workflow, error) {
	if workflow.ID == "" {
		workflow.ID = uuid.NewString()
	}
	if workflow.DraftRevision == 0 {
		workflow.DraftRevision = 1
	}
	if len(workflow.DraftGraph) == 0 {
		workflow.DraftGraph = json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`)
	}
	presentation, err := json.Marshal(workflow.AgentPresentation)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("encode agent presentation: %w", err)
	}
	row := store.pool.QueryRow(ctx, `WITH inserted AS (
        INSERT INTO workflows(id,name,slug,description,agent_presentation,draft_graph,draft_revision)
        VALUES($1,$2,$3,$4,$5,$6,$7)
        RETURNING *
    )
    SELECT i.id::text,i.name,i.slug,i.description,i.agent_presentation,i.draft_graph,i.draft_revision,
           i.published_version_id::text,NULL::integer,i.archived_at,i.created_at,i.updated_at
    FROM inserted i`, workflow.ID, workflow.Name, workflow.Slug, workflow.Description, presentation, workflow.DraftGraph, workflow.DraftRevision)
	created, err := scanWorkflow(row)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "workflows_slug_key" {
			return domain.Workflow{}, domain.ErrSlugConflict
		}
		return domain.Workflow{}, fmt.Errorf("create workflow: %w", err)
	}
	return created, nil
}

func (store *Store) UpdateAgentPresentation(ctx context.Context, workflowID string, expectedRevision int64, presentation domain.AgentPresentation) (domain.Workflow, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("begin agent presentation update: %w", err)
	}
	defer transaction.Rollback(ctx)
	var actualRevision int64
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT draft_revision,archived_at FROM workflows WHERE id=$1 FOR UPDATE", workflowID).Scan(&actualRevision, &archivedAt); err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	if archivedAt != nil {
		return domain.Workflow{}, domain.ErrWorkflowArchived
	}
	if actualRevision != expectedRevision {
		return domain.Workflow{}, ErrRevisionConflict
	}
	encoded, err := json.Marshal(presentation)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("encode agent presentation: %w", err)
	}
	row := transaction.QueryRow(ctx, `WITH updated AS (
        UPDATE workflows
        SET agent_presentation=$3,draft_revision=draft_revision+1,updated_at=now()
        WHERE id=$1 AND draft_revision=$2 AND archived_at IS NULL
        RETURNING *
    )
    SELECT u.id::text,u.name,u.slug,u.description,u.agent_presentation,u.draft_graph,u.draft_revision,
           u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
    FROM updated u
    LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`, workflowID, expectedRevision, encoded)
	updated, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("update agent presentation: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.Workflow{}, fmt.Errorf("commit agent presentation update: %w", err)
	}
	return updated, nil
}

func (store *Store) GetWorkflow(ctx context.Context, workflowID string) (domain.Workflow, error) {
	row := store.pool.QueryRow(ctx, `SELECT `+workflowSelectColumns+`
        FROM workflows w
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.id=$1`, workflowID)
	workflow, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	return workflow, nil
}

func (store *Store) UpdateDraft(ctx context.Context, workflowID string, expectedRevision int64, graph json.RawMessage) (domain.Workflow, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("begin workflow draft update: %w", err)
	}
	defer transaction.Rollback(ctx)
	var actualRevision int64
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT draft_revision,archived_at FROM workflows WHERE id=$1 FOR UPDATE", workflowID).Scan(&actualRevision, &archivedAt); err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	if archivedAt != nil {
		return domain.Workflow{}, domain.ErrWorkflowArchived
	}
	if actualRevision != expectedRevision {
		return domain.Workflow{}, ErrRevisionConflict
	}
	row := transaction.QueryRow(ctx, `WITH updated AS (
        UPDATE workflows
        SET draft_graph=$3,draft_revision=draft_revision+1,updated_at=now()
        WHERE id=$1 AND draft_revision=$2 AND archived_at IS NULL
        RETURNING *
    )
    SELECT u.id::text,u.name,u.slug,u.description,u.agent_presentation,u.draft_graph,u.draft_revision,
           u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
    FROM updated u
    LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`, workflowID, expectedRevision, graph)
	workflow, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("update workflow draft: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.Workflow{}, fmt.Errorf("commit workflow draft update: %w", err)
	}
	return workflow, nil
}

type workflowScanner interface {
	Scan(...any) error
}

func scanWorkflow(row workflowScanner) (domain.Workflow, error) {
	var workflow domain.Workflow
	var presentation, graph []byte
	if err := row.Scan(
		&workflow.ID,
		&workflow.Name,
		&workflow.Slug,
		&workflow.Description,
		&presentation,
		&graph,
		&workflow.DraftRevision,
		&workflow.PublishedVersionID,
		&workflow.PublishedVersion,
		&workflow.ArchivedAt,
		&workflow.CreatedAt,
		&workflow.UpdatedAt,
	); err != nil {
		return domain.Workflow{}, err
	}
	workflow.DraftGraph = json.RawMessage(graph)
	if err := json.Unmarshal(presentation, &workflow.AgentPresentation); err != nil {
		return domain.Workflow{}, fmt.Errorf("decode agent presentation: %w", err)
	}
	return workflow, nil
}

var _ workflowScanner = (pgx.Row)(nil)
