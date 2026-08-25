package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) ListRunSummaries(ctx context.Context, query workflowservice.RunSummaryStoreQuery) ([]domain.RunSummary, error) {
	statement, arguments, err := buildRunSummaryQuery(query)
	if err != nil {
		return nil, err
	}
	rows, err := store.pool.Query(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list run summaries: %w", err)
	}
	defer rows.Close()
	summaries := make([]domain.RunSummary, 0)
	for rows.Next() {
		var summary domain.RunSummary
		if err := rows.Scan(
			&summary.ID,
			&summary.WorkflowID,
			&summary.WorkflowName,
			&summary.WorkflowSlug,
			&summary.WorkflowVersionID,
			&summary.WorkflowVersion,
			&summary.DraftRevision,
			&summary.SourceRunID,
			&summary.SourceNodeID,
			&summary.Mode,
			&summary.Status,
			&summary.StartedAt,
			&summary.EndedAt,
		); err != nil {
			return nil, fmt.Errorf("scan run summary: %w", err)
		}
		summary.StartedAt = summary.StartedAt.UTC()
		if summary.EndedAt != nil {
			endedAt := summary.EndedAt.UTC()
			summary.EndedAt = &endedAt
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list run summaries: %w", err)
	}
	return summaries, nil
}

func buildRunSummaryQuery(query workflowservice.RunSummaryStoreQuery) (string, []any, error) {
	if query.Limit < 1 || query.Limit > 101 || (query.AfterStarted == nil) != (query.AfterID == "") {
		return "", nil, workflowservice.ErrInvalidWorkflowInput
	}
	var err error
	if query.WorkflowID, err = canonicalStoreUUID(query.WorkflowID); err != nil {
		return "", nil, err
	}
	if query.RunID, err = canonicalStoreUUID(query.RunID); err != nil {
		return "", nil, err
	}
	if query.AfterID, err = canonicalStoreUUID(query.AfterID); err != nil {
		return "", nil, err
	}
	statuses, err := storeRunStatuses(query.Statuses)
	if err != nil {
		return "", nil, err
	}
	modes, err := storeRunModes(query.Modes)
	if err != nil {
		return "", nil, err
	}

	statement := strings.Builder{}
	statement.WriteString(`SELECT r.id::text,r.workflow_id::text,w.name,w.slug,r.workflow_version_id::text,
       rv.version,r.draft_revision,r.source_run_id::text,r.source_node_id,r.mode,r.status,
       r.started_at,r.ended_at
FROM runs r
JOIN workflows w ON w.id=r.workflow_id
LEFT JOIN workflow_versions rv ON rv.id=r.workflow_version_id AND rv.workflow_id=r.workflow_id
WHERE true`)
	arguments := make([]any, 0, 10)
	placeholder := func(value any) string {
		arguments = append(arguments, value)
		return fmt.Sprintf("$%d", len(arguments))
	}
	if query.WorkflowID != "" {
		statement.WriteString(" AND r.workflow_id=" + placeholder(query.WorkflowID) + "::uuid")
	}
	if len(statuses) == 1 {
		statement.WriteString(" AND r.status=" + placeholder(statuses[0]))
	} else if len(statuses) > 1 {
		statement.WriteString(" AND r.status=ANY(" + placeholder(statuses) + "::text[])")
	}
	if len(modes) == 1 {
		statement.WriteString(" AND r.mode=" + placeholder(modes[0]))
	} else if len(modes) > 1 {
		statement.WriteString(" AND r.mode=ANY(" + placeholder(modes) + "::text[])")
	}
	if query.StartedAfter != nil {
		statement.WriteString(" AND r.started_at >= " + placeholder(*query.StartedAfter))
	}
	if query.StartedBefore != nil {
		statement.WriteString(" AND r.started_at < " + placeholder(*query.StartedBefore))
	}
	if query.RunID != "" {
		statement.WriteString(" AND r.id=" + placeholder(query.RunID) + "::uuid")
	}
	if query.AfterStarted != nil {
		statement.WriteString(" AND (r.started_at,r.id) < (" + placeholder(*query.AfterStarted) + "," + placeholder(query.AfterID) + "::uuid)")
	}
	statement.WriteString(" ORDER BY r.started_at DESC,r.id DESC LIMIT " + placeholder(query.Limit))
	return statement.String(), arguments, nil
}

func canonicalStoreUUID(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", workflowservice.ErrInvalidWorkflowInput
	}
	return parsed.String(), nil
}

func storeRunStatuses(values []domain.RunStatus) ([]string, error) {
	result := make([]string, len(values))
	for index, value := range values {
		if value != domain.RunRunning && value != domain.RunCompleted && value != domain.RunFailed && value != domain.RunCancelled {
			return nil, workflowservice.ErrInvalidWorkflowInput
		}
		result[index] = string(value)
	}
	return result, nil
}

func storeRunModes(values []domain.RunMode) ([]string, error) {
	result := make([]string, len(values))
	for index, value := range values {
		if value != domain.RunModeTest && value != domain.RunModePublished && value != domain.RunModeDebug {
			return nil, workflowservice.ErrInvalidWorkflowInput
		}
		result[index] = string(value)
	}
	return result, nil
}
