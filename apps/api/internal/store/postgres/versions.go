package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"agentstudio.local/api/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (store *Store) Publish(ctx context.Context, workflowID string, expectedRevision int64, graph, inputSchema json.RawMessage) (domain.WorkflowVersion, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("begin publish: %w", err)
	}
	defer transaction.Rollback(ctx)
	var actualRevision int64
	if err := transaction.QueryRow(ctx, "SELECT draft_revision FROM workflows WHERE id=$1 FOR UPDATE", workflowID).Scan(&actualRevision); err != nil {
		return domain.WorkflowVersion{}, mapNotFound(err)
	}
	if actualRevision != expectedRevision {
		return domain.WorkflowVersion{}, ErrRevisionConflict
	}
	var nextVersion int
	if err := transaction.QueryRow(ctx, "SELECT COALESCE(MAX(version),0)+1 FROM workflow_versions WHERE workflow_id=$1", workflowID).Scan(&nextVersion); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("allocate workflow version: %w", err)
	}
	version := domain.WorkflowVersion{
		ID:          uuid.NewString(),
		WorkflowID:  workflowID,
		Version:     nextVersion,
		Graph:       graph,
		InputSchema: inputSchema,
	}
	if err := transaction.QueryRow(ctx, `INSERT INTO workflow_versions(id,workflow_id,version,graph,input_schema)
        VALUES($1,$2,$3,$4,$5) RETURNING created_at`, version.ID, version.WorkflowID, version.Version, version.Graph, version.InputSchema).Scan(&version.CreatedAt); err != nil {
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
        v.id::text,v.workflow_id::text,v.version,v.graph,v.input_schema,v.created_at
        FROM workflows w
        JOIN workflow_versions v ON v.workflow_id=w.id AND v.id=w.published_version_id
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.slug=$1`, slug)
	return scanWorkflowAndVersion(row)
}

func (store *Store) GetAgentVersion(ctx context.Context, slug, versionID string) (domain.Workflow, domain.WorkflowVersion, error) {
	row := store.pool.QueryRow(ctx, `SELECT `+workflowSelectColumns+`,
        v.id::text,v.workflow_id::text,v.version,v.graph,v.input_schema,v.created_at
        FROM workflows w
        JOIN workflow_versions v ON v.workflow_id=w.id AND v.id=$2
        LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
        WHERE w.slug=$1`, slug, versionID)
	return scanWorkflowAndVersion(row)
}

func scanWorkflowAndVersion(row pgx.Row) (domain.Workflow, domain.WorkflowVersion, error) {
	var workflow domain.Workflow
	var version domain.WorkflowVersion
	var draftGraph, versionGraph, inputSchema []byte
	if err := row.Scan(
		&workflow.ID, &workflow.Name, &workflow.Slug, &workflow.Description, &draftGraph,
		&workflow.DraftRevision, &workflow.PublishedVersionID, &workflow.PublishedVersion,
		&workflow.CreatedAt, &workflow.UpdatedAt,
		&version.ID, &version.WorkflowID, &version.Version, &versionGraph, &inputSchema, &version.CreatedAt,
	); err != nil {
		return domain.Workflow{}, domain.WorkflowVersion{}, mapNotFound(err)
	}
	workflow.DraftGraph = json.RawMessage(draftGraph)
	version.Graph = json.RawMessage(versionGraph)
	version.InputSchema = json.RawMessage(inputSchema)
	return workflow, version, nil
}
