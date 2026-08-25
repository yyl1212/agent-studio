package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) ListWorkflowSummaries(ctx context.Context, query workflowservice.WorkflowSummaryStoreQuery) ([]domain.WorkflowSummary, error) {
	statement, arguments, err := buildWorkflowSummaryQuery(query)
	if err != nil {
		return nil, err
	}
	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list workflow summaries: %w", err)
	}
	defer rows.Close()
	summaries := make([]domain.WorkflowSummary, 0)
	for rows.Next() {
		var summary domain.WorkflowSummary
		if err := rows.Scan(
			&summary.ID,
			&summary.Name,
			&summary.Slug,
			&summary.Description,
			&summary.DraftRevision,
			&summary.PublishedVersionID,
			&summary.PublishedVersion,
			&summary.ArchivedAt,
			&summary.CreatedAt,
			&summary.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan workflow summary: %w", err)
		}
		summary.CreatedAt = summary.CreatedAt.UTC()
		summary.UpdatedAt = summary.UpdatedAt.UTC()
		if summary.ArchivedAt != nil {
			archivedAt := summary.ArchivedAt.UTC()
			summary.ArchivedAt = &archivedAt
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list workflow summaries: %w", err)
	}
	return summaries, nil
}

func buildWorkflowSummaryQuery(query workflowservice.WorkflowSummaryStoreQuery) (string, []any, error) {
	if query.Limit < 1 || query.Limit > 101 {
		return "", nil, workflowservice.ErrInvalidWorkflowInput
	}
	if (query.AfterUpdated == nil) != (query.AfterID == "") {
		return "", nil, workflowservice.ErrInvalidWorkflowInput
	}
	if query.AfterID != "" {
		parsedID, err := uuid.Parse(query.AfterID)
		if err != nil {
			return "", nil, workflowservice.ErrInvalidWorkflowInput
		}
		query.AfterID = parsedID.String()
	}

	statement := strings.Builder{}
	statement.WriteString(`SELECT w.id::text,w.name,w.slug,w.description,w.draft_revision,
       w.published_version_id::text,pv.version,w.archived_at,w.created_at,w.updated_at
FROM workflows w
LEFT JOIN workflow_versions pv ON pv.workflow_id=w.id AND pv.id=w.published_version_id
WHERE true`)
	arguments := make([]any, 0, 5)
	switch query.State {
	case workflowservice.WorkflowStateActive:
		statement.WriteString(" AND w.archived_at IS NULL")
	case workflowservice.WorkflowStateArchived:
		statement.WriteString(" AND w.archived_at IS NOT NULL")
	case workflowservice.WorkflowStateAll:
	default:
		return "", nil, workflowservice.ErrInvalidWorkflowInput
	}
	if query.Text != "" {
		arguments = append(arguments, escapeLikePattern(query.Text))
		placeholder := fmt.Sprintf("$%d", len(arguments))
		statement.WriteString(" AND (w.name ILIKE '%' || " + placeholder + " || '%' ESCAPE E'\\\\' OR w.slug ILIKE '%' || " + placeholder + " || '%' ESCAPE E'\\\\')")
	}
	if query.AfterUpdated != nil {
		arguments = append(arguments, *query.AfterUpdated)
		timePlaceholder := fmt.Sprintf("$%d", len(arguments))
		arguments = append(arguments, query.AfterID)
		idPlaceholder := fmt.Sprintf("$%d", len(arguments))
		statement.WriteString(" AND (w.updated_at,w.id) < (" + timePlaceholder + "," + idPlaceholder + "::uuid)")
	}
	arguments = append(arguments, query.Limit)
	statement.WriteString(fmt.Sprintf(" ORDER BY w.updated_at DESC,w.id DESC LIMIT $%d", len(arguments)))
	return statement.String(), arguments, nil
}

func escapeLikePattern(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func (store *Store) UpdateWorkflowMetadata(ctx context.Context, id, name, description string) (domain.Workflow, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("begin workflow metadata update: %w", err)
	}
	defer transaction.Rollback(ctx)
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT archived_at FROM workflows WHERE id=$1 FOR UPDATE", id).Scan(&archivedAt); err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	if archivedAt != nil {
		return domain.Workflow{}, domain.ErrWorkflowArchived
	}
	row := transaction.QueryRow(ctx, `WITH updated AS (
    UPDATE workflows SET name=$2,description=$3,updated_at=now()
    WHERE id=$1 AND archived_at IS NULL RETURNING *
)
SELECT u.id::text,u.name,u.slug,u.description,u.draft_graph,u.draft_revision,
       u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
FROM updated u
LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`, id, name, description)
	updated, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("update workflow metadata: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.Workflow{}, fmt.Errorf("commit workflow metadata update: %w", err)
	}
	return updated, nil
}

func (store *Store) ArchiveWorkflow(ctx context.Context, id string) (domain.Workflow, error) {
	row := store.pool.QueryRow(ctx, `WITH updated AS (
    UPDATE workflows SET
      archived_at=COALESCE(archived_at,now()),
      updated_at=CASE WHEN archived_at IS NULL THEN now() ELSE updated_at END
    WHERE id=$1 RETURNING *
)
SELECT u.id::text,u.name,u.slug,u.description,u.draft_graph,u.draft_revision,
       u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
FROM updated u
LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`, id)
	archived, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	return archived, nil
}

func (store *Store) RestoreWorkflow(ctx context.Context, id string) (domain.Workflow, error) {
	row := store.pool.QueryRow(ctx, `WITH updated AS (
    UPDATE workflows SET
      archived_at=NULL,
      updated_at=CASE WHEN archived_at IS NOT NULL THEN now() ELSE updated_at END
    WHERE id=$1 RETURNING *
)
SELECT u.id::text,u.name,u.slug,u.description,u.draft_graph,u.draft_revision,
       u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
FROM updated u
LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`, id)
	restored, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	return restored, nil
}
