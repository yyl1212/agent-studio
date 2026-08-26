package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) ListWorkflowVersions(ctx context.Context, workflowID string, beforeVersion, limit int) (workflowservice.VersionListRows, error) {
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return workflowservice.VersionListRows{}, fmt.Errorf("begin workflow version list: %w", err)
	}
	defer transaction.Rollback(ctx)

	var draftRevision int64
	var publishedVersionID *string
	if err := transaction.QueryRow(ctx, `SELECT draft_revision,published_version_id::text
		FROM workflows WHERE id=$1`, workflowID).Scan(&draftRevision, &publishedVersionID); err != nil {
		return workflowservice.VersionListRows{}, mapNotFound(err)
	}

	rows, err := transaction.Query(ctx, `SELECT id::text,version,created_at
		FROM workflow_versions
		WHERE workflow_id=$1 AND ($2=0 OR version<$2)
		ORDER BY version DESC
		LIMIT $3`, workflowID, beforeVersion, limit)
	if err != nil {
		return workflowservice.VersionListRows{}, fmt.Errorf("list workflow versions: %w", err)
	}
	items := make([]domain.WorkflowVersionSummary, 0)
	for rows.Next() {
		var item domain.WorkflowVersionSummary
		if err := rows.Scan(&item.ID, &item.Version, &item.CreatedAt); err != nil {
			rows.Close()
			return workflowservice.VersionListRows{}, fmt.Errorf("scan workflow version summary: %w", err)
		}
		item.Current = publishedVersionID != nil && item.ID == *publishedVersionID
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return workflowservice.VersionListRows{}, fmt.Errorf("iterate workflow versions: %w", err)
	}
	rows.Close()

	var checkpoint *domain.RollbackCheckpointSummary
	var loaded domain.RollbackCheckpointSummary
	err = transaction.QueryRow(ctx, `SELECT c.source_revision,c.restored_revision,v.version,c.created_at
		FROM workflow_draft_checkpoints c
		JOIN workflow_versions v ON v.workflow_id=c.workflow_id AND v.id=c.restored_from_version_id
		WHERE c.workflow_id=$1 AND c.restored_revision=$2`, workflowID, draftRevision).Scan(
		&loaded.SourceRevision,
		&loaded.RestoredRevision,
		&loaded.RestoredFromVersion,
		&loaded.CreatedAt,
	)
	if err == nil {
		checkpoint = &loaded
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return workflowservice.VersionListRows{}, fmt.Errorf("load workflow rollback checkpoint: %w", err)
	}

	if err := transaction.Commit(ctx); err != nil {
		return workflowservice.VersionListRows{}, fmt.Errorf("commit workflow version list: %w", err)
	}
	return workflowservice.VersionListRows{Items: items, Checkpoint: checkpoint}, nil
}
