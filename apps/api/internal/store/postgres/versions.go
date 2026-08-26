package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func (store *Store) Publish(ctx context.Context, workflowID string, expectedRevision int64, graph, inputSchema json.RawMessage, presentation domain.AgentPresentation) (domain.WorkflowVersion, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("begin publish: %w", err)
	}
	defer transaction.Rollback(ctx)
	var actualRevision int64
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT draft_revision,archived_at FROM workflows WHERE id=$1 FOR UPDATE", workflowID).Scan(&actualRevision, &archivedAt); err != nil {
		return domain.WorkflowVersion{}, mapNotFound(err)
	}
	if archivedAt != nil {
		return domain.WorkflowVersion{}, domain.ErrWorkflowArchived
	}
	if actualRevision != expectedRevision {
		return domain.WorkflowVersion{}, ErrRevisionConflict
	}
	var nextVersion int
	if err := transaction.QueryRow(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM workflow_versions WHERE workflow_id=$1", workflowID).Scan(&nextVersion); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("allocate workflow version: %w", err)
	}
	version := domain.WorkflowVersion{
		ID:                uuid.NewString(),
		WorkflowID:        workflowID,
		Version:           nextVersion,
		Graph:             graph,
		InputSchema:       inputSchema,
		AgentPresentation: presentation,
	}
	encodedPresentation, err := json.Marshal(presentation)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("encode agent presentation: %w", err)
	}
	if err := transaction.QueryRow(ctx, `INSERT INTO workflow_versions(id,workflow_id,version,graph,input_schema,agent_presentation)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, version.ID, version.WorkflowID, version.Version, version.Graph, version.InputSchema, encodedPresentation).Scan(&version.CreatedAt); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("insert workflow version: %w", err)
	}
	if _, err := transaction.Exec(ctx, "UPDATE workflows SET published_version_id=$2,updated_at=now() WHERE id=$1", workflowID, version.ID); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("set published version: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("commit publish: %w", err)
	}
	return version, nil
}

func (store *Store) GetCurrentAgentVersion(ctx context.Context, slug string) (domain.Workflow, domain.WorkflowVersion, error) {
	row := store.pool.QueryRow(ctx, `SELECT `+workflowSelectColumns+`,
		v.id::text,v.workflow_id::text,v.version,v.graph,v.input_schema,v.agent_presentation,v.created_at
        FROM workflows w
        JOIN workflow_versions v ON v.workflow_id=w.id AND v.id=w.published_version_id
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.slug=$1`, slug)
	return scanWorkflowAndVersion(row)
}

func (store *Store) GetAgentVersion(ctx context.Context, slug, versionID string) (domain.Workflow, domain.WorkflowVersion, error) {
	row := store.pool.QueryRow(ctx, `SELECT `+workflowSelectColumns+`,
		v.id::text,v.workflow_id::text,v.version,v.graph,v.input_schema,v.agent_presentation,v.created_at
        FROM workflows w
        JOIN workflow_versions v ON v.workflow_id=w.id AND v.id=$2
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.slug=$1`, slug, versionID)
	return scanWorkflowAndVersion(row)
}

func scanWorkflowAndVersion(row pgx.Row) (domain.Workflow, domain.WorkflowVersion, error) {
	var workflow domain.Workflow
	var version domain.WorkflowVersion
	var workflowPresentation, draftGraph, versionGraph, inputSchema, versionPresentation []byte
	if err := row.Scan(
		&workflow.ID, &workflow.Name, &workflow.Slug, &workflow.Description, &workflowPresentation, &draftGraph,
		&workflow.DraftRevision, &workflow.PublishedVersionID, &workflow.PublishedVersion,
		&workflow.ArchivedAt, &workflow.CreatedAt, &workflow.UpdatedAt,
		&version.ID, &version.WorkflowID, &version.Version, &versionGraph, &inputSchema, &versionPresentation, &version.CreatedAt,
	); err != nil {
		return domain.Workflow{}, domain.WorkflowVersion{}, mapNotFound(err)
	}
	workflow.DraftGraph = json.RawMessage(draftGraph)
	version.Graph = json.RawMessage(versionGraph)
	version.InputSchema = json.RawMessage(inputSchema)
	if err := json.Unmarshal(workflowPresentation, &workflow.AgentPresentation); err != nil {
		return domain.Workflow{}, domain.WorkflowVersion{}, fmt.Errorf("decode workflow agent presentation: %w", err)
	}
	if err := json.Unmarshal(versionPresentation, &version.AgentPresentation); err != nil {
		return domain.Workflow{}, domain.WorkflowVersion{}, fmt.Errorf("decode version agent presentation: %w", err)
	}
	return workflow, version, nil
}
