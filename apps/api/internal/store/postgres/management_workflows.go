package postgres

import (
	"context"
	"fmt"
	"strings"

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
