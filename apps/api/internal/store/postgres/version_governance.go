package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) GetWorkflowVersionByNumber(ctx context.Context, workflowID string, versionNumber int) (domain.WorkflowVersion, error) {
	row := store.pool.QueryRow(ctx, `SELECT id::text,workflow_id::text,version,graph,input_schema,agent_presentation,created_at
		FROM workflow_versions
		WHERE workflow_id=$1 AND version=$2`, workflowID, versionNumber)
	var version domain.WorkflowVersion
	var graph, inputSchema, presentation []byte
	if err := row.Scan(
		&version.ID,
		&version.WorkflowID,
		&version.Version,
		&graph,
		&inputSchema,
		&presentation,
		&version.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkflowVersion{}, domain.ErrWorkflowVersionNotFound
		}
		return domain.WorkflowVersion{}, fmt.Errorf("load workflow version by number: %w", err)
	}
	version.Graph = append(json.RawMessage(nil), graph...)
	version.InputSchema = append(json.RawMessage(nil), inputSchema...)
	if err := json.Unmarshal(presentation, &version.AgentPresentation); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("decode workflow version presentation: %w", err)
	}
	return version, nil
}

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

func (store *Store) RollbackWorkflowDraft(ctx context.Context, workflowID, versionID string, expectedRevision int64) (domain.Workflow, domain.RollbackCheckpointSummary, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, fmt.Errorf("begin workflow draft rollback: %w", err)
	}
	defer transaction.Rollback(ctx)

	var actualRevision int64
	var archived bool
	var sourceGraph, sourcePresentation []byte
	if err := transaction.QueryRow(ctx, `SELECT draft_revision,archived_at IS NOT NULL,draft_graph,agent_presentation
		FROM workflows WHERE id=$1 FOR UPDATE`, workflowID).Scan(
		&actualRevision,
		&archived,
		&sourceGraph,
		&sourcePresentation,
	); err != nil {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, mapNotFound(err)
	}
	if archived {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, domain.ErrWorkflowArchived
	}
	if actualRevision != expectedRevision {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, domain.ErrRevisionConflict
	}

	var targetVersion int
	var targetGraph, targetPresentation []byte
	if err := transaction.QueryRow(ctx, `SELECT version,graph,agent_presentation
		FROM workflow_versions WHERE workflow_id=$1 AND id=$2`, workflowID, versionID).Scan(
		&targetVersion,
		&targetGraph,
		&targetPresentation,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Workflow{}, domain.RollbackCheckpointSummary{}, domain.ErrWorkflowVersionNotFound
		}
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, fmt.Errorf("load workflow rollback version: %w", err)
	}

	checkpoint := domain.RollbackCheckpointSummary{
		SourceRevision: actualRevision, RestoredRevision: actualRevision + 1, RestoredFromVersion: targetVersion,
	}
	if err := transaction.QueryRow(ctx, `INSERT INTO workflow_draft_checkpoints(
		workflow_id,source_revision,restored_revision,graph,agent_presentation,restored_from_version_id
	) VALUES($1,$2,$3,$4,$5,$6)
	ON CONFLICT(workflow_id) DO UPDATE SET
		source_revision=excluded.source_revision,
		restored_revision=excluded.restored_revision,
		graph=excluded.graph,
		agent_presentation=excluded.agent_presentation,
		restored_from_version_id=excluded.restored_from_version_id,
		created_at=now()
	RETURNING created_at`, workflowID, checkpoint.SourceRevision, checkpoint.RestoredRevision,
		sourceGraph, sourcePresentation, versionID).Scan(&checkpoint.CreatedAt); err != nil {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, fmt.Errorf("save workflow rollback checkpoint: %w", err)
	}

	row := transaction.QueryRow(ctx, `WITH updated AS (
		UPDATE workflows
		SET draft_graph=$3,agent_presentation=$4,draft_revision=draft_revision+1,updated_at=now()
		WHERE id=$1 AND draft_revision=$2 AND archived_at IS NULL
		RETURNING *
	)
	SELECT u.id::text,u.name,u.slug,u.description,u.agent_presentation,u.draft_graph,u.draft_revision,
		u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
	FROM updated u
	LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`,
		workflowID, actualRevision, targetGraph, targetPresentation)
	workflow, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, fmt.Errorf("rollback workflow draft: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.Workflow{}, domain.RollbackCheckpointSummary{}, fmt.Errorf("commit workflow draft rollback: %w", err)
	}
	return workflow, checkpoint, nil
}

func (store *Store) UndoWorkflowDraftRollback(ctx context.Context, workflowID string, expectedRevision int64) (domain.Workflow, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("begin workflow draft rollback undo: %w", err)
	}
	defer transaction.Rollback(ctx)

	var actualRevision int64
	var archived bool
	if err := transaction.QueryRow(ctx, `SELECT draft_revision,archived_at IS NOT NULL
		FROM workflows WHERE id=$1 FOR UPDATE`, workflowID).Scan(&actualRevision, &archived); err != nil {
		return domain.Workflow{}, mapNotFound(err)
	}
	if archived {
		return domain.Workflow{}, domain.ErrWorkflowArchived
	}
	if actualRevision != expectedRevision {
		return domain.Workflow{}, domain.ErrRevisionConflict
	}

	var restoredRevision int64
	var sourceGraph, sourcePresentation []byte
	if err := transaction.QueryRow(ctx, `SELECT restored_revision,graph,agent_presentation
		FROM workflow_draft_checkpoints WHERE workflow_id=$1`, workflowID).Scan(
		&restoredRevision,
		&sourceGraph,
		&sourcePresentation,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Workflow{}, domain.ErrRollbackUndoUnavailable
		}
		return domain.Workflow{}, fmt.Errorf("load workflow rollback checkpoint for undo: %w", err)
	}
	if restoredRevision != actualRevision {
		return domain.Workflow{}, domain.ErrRollbackUndoUnavailable
	}

	row := transaction.QueryRow(ctx, `WITH updated AS (
		UPDATE workflows
		SET draft_graph=$3,agent_presentation=$4,draft_revision=draft_revision+1,updated_at=now()
		WHERE id=$1 AND draft_revision=$2 AND archived_at IS NULL
		RETURNING *
	)
	SELECT u.id::text,u.name,u.slug,u.description,u.agent_presentation,u.draft_graph,u.draft_revision,
		u.published_version_id::text,pv.version,u.archived_at,u.created_at,u.updated_at
	FROM updated u
	LEFT JOIN workflow_versions pv ON pv.workflow_id=u.id AND pv.id=u.published_version_id`,
		workflowID, actualRevision, sourceGraph, sourcePresentation)
	workflow, err := scanWorkflow(row)
	if err != nil {
		return domain.Workflow{}, fmt.Errorf("undo workflow draft rollback: %w", err)
	}
	if _, err := transaction.Exec(ctx, "DELETE FROM workflow_draft_checkpoints WHERE workflow_id=$1", workflowID); err != nil {
		return domain.Workflow{}, fmt.Errorf("delete workflow rollback checkpoint: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.Workflow{}, fmt.Errorf("commit workflow draft rollback undo: %w", err)
	}
	return workflow, nil
}
