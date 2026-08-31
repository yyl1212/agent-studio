package backup

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yyl1212/agent-studio/internal/database"
)

type RestoreResult struct {
	Summary          Summary              `json:"archive"`
	MigrationVersion int64                `json:"databaseMigrationVersion"`
	Tables           map[TableName]uint64 `json:"tables"`
}

type restoreHooks struct {
	afterPreflight func() error
	afterTable     func(TableName) error
}

func Restore(ctx context.Context, pool *pgxpool.Pool, path string) (RestoreResult, error) {
	result, err := restoreWithHooks(ctx, pool, path, restoreHooks{})
	return result, sanitizePublicBackupError(err)
}

func restoreWithHooks(ctx context.Context, pool *pgxpool.Pool, path string, hooks restoreHooks) (RestoreResult, error) {
	archive, err := OpenArchive(ctx, path)
	if err != nil {
		return RestoreResult{}, err
	}
	defer archive.Close()

	lease, err := database.TryExclusive(ctx, pool)
	if errors.Is(err, database.ErrMaintenanceBusy) {
		return RestoreResult{}, Wrap(CodeAPIRunning, "target is in use", nil)
	}
	if err != nil {
		return RestoreResult{}, Wrap(CodeRestoreFailed, "acquire maintenance lease", nil)
	}
	defer lease.Release(context.Background())
	restoreContext, stopMonitoring := monitorLeaseLoss(ctx, lease.MonitorConnectionLoss())
	defer stopMonitoring()

	plan, err := preflightWithLease(restoreContext, lease, archive)
	if err != nil {
		return RestoreResult{}, normalizeRestoreContextError(ctx, restoreContext, err)
	}
	if hooks.afterPreflight != nil {
		if err := hooks.afterPreflight(); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := lease.Migrate(restoreContext); err != nil {
		return RestoreResult{}, normalizeRestoreContextError(ctx, restoreContext,
			wrapRestoreFailure("migrate empty target", err))
	}
	key := importerKey{APIVersion: archive.manifest.APIVersion, MigrationVersion: archive.manifest.DatabaseMigrationVersion}
	importArchive := importers[key]
	if importArchive == nil {
		return RestoreResult{}, Wrap(CodeFormatUnsupported, "backup migration is not supported", nil)
	}
	result, err := importArchive(restoreContext, lease, archive, plan, hooks)
	if err != nil {
		return RestoreResult{}, normalizeRestoreContextError(ctx, restoreContext, err)
	}
	return result, nil
}

func normalizeRestoreContextError(parent, restoreContext context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if cause := context.Cause(restoreContext); cause != nil {
		return Wrap(CodeRestoreFailed, "target maintenance lease lost", nil)
	}
	return err
}

func importV1Alpha1Migration6(
	ctx context.Context,
	lease *database.MaintenanceLease,
	archive *Archive,
	plan RestorePlan,
	hooks restoreHooks,
) (RestoreResult, error) {
	return restoreTransaction(ctx, lease, archive, plan, hooks)
}

func restoreTransaction(
	ctx context.Context,
	lease *database.MaintenanceLease,
	archive *Archive,
	plan RestorePlan,
	hooks restoreHooks,
) (RestoreResult, error) {
	transaction, err := lease.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RestoreResult{}, wrapRestoreFailure("begin restore transaction", err)
	}
	defer transaction.Rollback(context.Background())

	empty, err := targetIsEmpty(ctx, transaction)
	if err != nil {
		return RestoreResult{}, wrapRestoreFailure("check migrated target contents", err)
	}
	if !empty {
		return RestoreResult{}, Wrap(CodeTargetNotEmpty, "target changed before restore", nil)
	}
	currentVersion, err := database.CurrentVersion(ctx, transaction)
	if err != nil {
		return RestoreResult{}, wrapRestoreFailure("read migrated target version", err)
	}
	if currentVersion != plan.LatestMigrationVersion {
		return RestoreResult{}, Wrap(CodeRestoreFailed, "validate migrated target version", nil)
	}

	counts := make(map[TableName]uint64, len(TableOrder))
	counts[TableWorkflows], err = copyWorkflows(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableWorkflows); err != nil {
		return RestoreResult{}, err
	}
	if err := copyPublishedVersionMappings(ctx, transaction, archive); err != nil {
		return RestoreResult{}, err
	}

	counts[TableWorkflowVersions], err = copyWorkflowVersions(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := restorePublishedVersions(ctx, transaction); err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableWorkflowVersions); err != nil {
		return RestoreResult{}, err
	}

	counts[TableRuns], err = copyRuns(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableRuns); err != nil {
		return RestoreResult{}, err
	}
	counts[TableNodeRuns], err = copyNodeRuns(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableNodeRuns); err != nil {
		return RestoreResult{}, err
	}
	counts[TableRunEvents], err = copyRunEvents(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableRunEvents); err != nil {
		return RestoreResult{}, err
	}
	counts[TableWorkflowDraftCheckpoints], err = copyWorkflowDraftCheckpoints(ctx, transaction, archive)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := callAfterTable(hooks, TableWorkflowDraftCheckpoints); err != nil {
		return RestoreResult{}, err
	}

	if err := validateRestoredData(ctx, transaction, archive, counts); err != nil {
		return RestoreResult{}, err
	}
	var alive int
	if err := transaction.QueryRow(ctx, "SELECT 1").Scan(&alive); err != nil || alive != 1 {
		return RestoreResult{}, wrapRestoreFailure("verify maintenance session before commit", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return RestoreResult{}, wrapRestoreFailure("commit restored backup", err)
	}
	return RestoreResult{Summary: archive.Summary(), MigrationVersion: currentVersion, Tables: counts}, nil
}

func callAfterTable(hooks restoreHooks, table TableName) error {
	if hooks.afterTable == nil {
		return nil
	}
	return hooks.afterTable(table)
}

func copyWorkflows(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableWorkflows,
		[]string{"id", "name", "slug", "description", "draft_graph", "draft_revision", "published_version_id", "created_at", "updated_at", "archived_at", "agent_presentation"},
		decodeWorkflowRecord,
		func(record WorkflowRecord) ([]any, error) {
			return []any{referenceUUID(record.ID), record.Name, record.Slug, record.Description, record.DraftGraph,
				record.DraftRevision, nil, record.CreatedAt, record.UpdatedAt, record.ArchivedAt, record.AgentPresentation}, nil
		})
}

func copyPublishedVersionMappings(ctx context.Context, tx pgx.Tx, archive *Archive) error {
	if _, err := tx.Exec(ctx, `CREATE TEMP TABLE backup_published_versions(
workflow_id uuid PRIMARY KEY, version_id uuid NULL
) ON COMMIT DROP`); err != nil {
		return wrapRestoreFailure("create published version mappings", err)
	}
	count, err := copyRecordsTo(ctx, tx, archive, TableWorkflows, "backup_published_versions",
		[]string{"workflow_id", "version_id"}, decodeWorkflowRecord,
		func(record WorkflowRecord) ([]any, error) {
			return []any{referenceUUID(record.ID), optionalReferenceUUID(record.PublishedVersionID)}, nil
		})
	if err != nil {
		return err
	}
	if count != archive.table(TableWorkflows).Records {
		return Wrap(CodeReferenceInvalid, "validate published version mappings", nil)
	}
	return nil
}

func copyWorkflowVersions(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableWorkflowVersions,
		[]string{"id", "workflow_id", "version", "graph", "input_schema", "created_at", "agent_presentation"},
		decodeWorkflowVersionRecord,
		func(record WorkflowVersionRecord) ([]any, error) {
			return []any{referenceUUID(record.ID), referenceUUID(record.WorkflowID), record.Version, record.Graph,
				record.InputSchema, record.CreatedAt, record.AgentPresentation}, nil
		})
}

func restorePublishedVersions(ctx context.Context, tx pgx.Tx) error {
	var expected int64
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM backup_published_versions WHERE version_id IS NOT NULL").Scan(&expected); err != nil {
		return wrapRestoreFailure("count published version mappings", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE workflows workflow SET published_version_id=mapping.version_id
FROM backup_published_versions mapping
WHERE workflow.id=mapping.workflow_id AND mapping.version_id IS NOT NULL`)
	if err != nil {
		return wrapRestoreFailure("restore published workflow versions", err)
	}
	if tag.RowsAffected() != expected {
		return Wrap(CodeReferenceInvalid, "validate published workflow versions", nil)
	}
	return nil
}

func copyRuns(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableRuns,
		[]string{"id", "workflow_id", "workflow_version_id", "draft_revision", "graph_snapshot", "mode", "status", "input", "output", "error", "started_at", "ended_at", "source_run_id", "source_node_id", "retry_of_run_id", "retry_key", "input_redacted_paths", "cancel_requested_at", "heartbeat_at", "agent_request_key"},
		decodeRunRecord,
		func(record RunRecord) ([]any, error) {
			return []any{referenceUUID(record.ID), referenceUUID(record.WorkflowID), optionalReferenceUUID(record.WorkflowVersionID),
				record.DraftRevision, record.GraphSnapshot.databaseValue(), record.Mode, record.Status, record.Input,
				record.Output.databaseValue(), record.Error.databaseValue(), record.StartedAt, record.EndedAt,
				optionalReferenceUUID(record.SourceRunID), record.SourceNodeID, optionalReferenceUUID(record.RetryOfRunID),
				optionalReferenceUUID(record.RetryKey), record.InputRedactedPaths, record.CancelRequestedAt, record.HeartbeatAt,
				optionalReferenceUUID(record.AgentRequestKey)}, nil
		})
}

func copyNodeRuns(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableNodeRuns,
		[]string{"id", "run_id", "node_id", "node_type", "status", "input", "output", "error", "started_at", "ended_at"},
		decodeNodeRunRecord,
		func(record NodeRunRecord) ([]any, error) {
			return []any{referenceUUID(record.ID), referenceUUID(record.RunID), record.NodeID, record.NodeType, record.Status,
				record.Input.databaseValue(), record.Output.databaseValue(), record.Error.databaseValue(), record.StartedAt, record.EndedAt}, nil
		})
}

func copyRunEvents(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableRunEvents,
		[]string{"run_id", "sequence", "type", "node_id", "status", "input", "output", "active_ports", "error", "input_redacted_paths", "output_redacted_paths", "data_bytes", "timestamp"},
		decodeRunEventRecord,
		func(record RunEventRecord) ([]any, error) {
			return []any{referenceUUID(record.RunID), record.Sequence, record.Type, record.NodeID, record.Status,
				record.Input.databaseValue(), record.Output.databaseValue(), record.ActivePorts, record.Error.databaseValue(),
				record.InputRedactedPaths, record.OutputRedactedPaths, record.DataBytes, record.Timestamp}, nil
		})
}

func copyWorkflowDraftCheckpoints(ctx context.Context, tx pgx.Tx, archive *Archive) (uint64, error) {
	return copyRecords(ctx, tx, archive, TableWorkflowDraftCheckpoints,
		[]string{"workflow_id", "source_revision", "restored_revision", "graph", "agent_presentation", "restored_from_version_id", "created_at"},
		decodeWorkflowDraftCheckpointRecord,
		func(record WorkflowDraftCheckpointRecord) ([]any, error) {
			return []any{referenceUUID(record.WorkflowID), record.SourceRevision, record.RestoredRevision, record.Graph,
				record.AgentPresentation, referenceUUID(record.RestoredFromVersionID), record.CreatedAt}, nil
		})
}

func copyRecords[T any](
	ctx context.Context,
	tx pgx.Tx,
	archive *Archive,
	table TableName,
	columns []string,
	decode func(json.RawMessage) (T, error),
	values func(T) ([]any, error),
) (uint64, error) {
	return copyRecordsTo(ctx, tx, archive, table, string(table), columns, decode, values)
}

func copyRecordsTo[T any](
	ctx context.Context,
	tx pgx.Tx,
	archive *Archive,
	table TableName,
	target string,
	columns []string,
	decode func(json.RawMessage) (T, error),
	values func(T) ([]any, error),
) (count uint64, resultErr error) {
	reader, err := archive.OpenTable(table)
	if err != nil {
		return 0, err
	}
	source := newRecordSource(ctx, reader, decode, values)
	defer func() {
		resultErr = combineCopyAndCloseErrors(resultErr, source.Close())
	}()
	copied, copyErr := tx.CopyFrom(ctx, pgx.Identifier{target}, columns, source)
	if sourceErr := source.Err(); sourceErr != nil {
		return 0, sourceErr
	}
	if copyErr != nil {
		return 0, wrapRestoreFailure("copy backup table "+string(table), copyErr)
	}
	if copied < 0 {
		return 0, Wrap(CodeRestoreFailed, "validate restored table count", nil)
	}
	count = uint64(copied)
	if count != archive.table(table).Records {
		return 0, Wrap(CodeReferenceInvalid, "validate restored table count", nil)
	}
	return count, nil
}

func combineCopyAndCloseErrors(resultErr, closeErr error) error {
	if closeErr == nil {
		return resultErr
	}
	var safeClose error
	if CodeOf(closeErr) != "" {
		safeClose = sanitizePublicBackupError(closeErr)
	} else {
		safeClose = Wrap(CodeArchiveInvalid, "close backup table", nil)
	}
	if resultErr == nil {
		return safeClose
	}
	safeResult := sanitizePublicBackupError(resultErr)
	// Keep a specific archive diagnosis primary; otherwise the close validation
	// outranks a generic database/values failure while both remain discoverable.
	if code := CodeOf(safeResult); code != "" && code != CodeRestoreFailed {
		return errors.Join(safeResult, safeClose)
	}
	return errors.Join(safeClose, safeResult)
}

func validateRestoredData(ctx context.Context, tx pgx.Tx, archive *Archive, counts map[TableName]uint64) error {
	for _, table := range TableOrder {
		var actual uint64
		statement := "SELECT count(*) FROM " + pgx.Identifier{string(table)}.Sanitize()
		if err := tx.QueryRow(ctx, statement).Scan(&actual); err != nil {
			return wrapRestoreFailure("count restored table", err)
		}
		if actual != counts[table] || actual != archive.table(table).Records {
			return Wrap(CodeReferenceInvalid, "validate restored table counts", nil)
		}
	}
	if _, err := stageReferences(ctx, tx, archive); err != nil {
		return err
	}
	for _, query := range restoredRelationshipQueries {
		var invalid bool
		if err := tx.QueryRow(ctx, query).Scan(&invalid); err != nil {
			return wrapRestoreFailure("validate restored relationships", err)
		}
		if invalid {
			return Wrap(CodeReferenceInvalid, "validate restored relationships", nil)
		}
	}
	return nil
}

var restoredRelationshipQueries = []string{
	`SELECT EXISTS (SELECT 1 FROM workflow_versions version WHERE NOT EXISTS (
SELECT 1 FROM workflows workflow WHERE workflow.id=version.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM workflows workflow WHERE workflow.published_version_id IS NOT NULL AND NOT EXISTS (
SELECT 1 FROM workflow_versions version WHERE version.id=workflow.published_version_id AND version.workflow_id=workflow.id))`,
	`SELECT EXISTS (SELECT 1 FROM runs run WHERE NOT EXISTS (
SELECT 1 FROM workflows workflow WHERE workflow.id=run.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM runs run WHERE run.workflow_version_id IS NOT NULL AND NOT EXISTS (
SELECT 1 FROM workflow_versions version WHERE version.id=run.workflow_version_id AND version.workflow_id=run.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM runs child WHERE child.source_run_id IS NOT NULL AND NOT EXISTS (
SELECT 1 FROM runs parent WHERE parent.id=child.source_run_id AND parent.workflow_id=child.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM runs child WHERE child.retry_of_run_id IS NOT NULL AND NOT EXISTS (
SELECT 1 FROM runs parent WHERE parent.id=child.retry_of_run_id AND parent.workflow_id=child.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM node_runs node WHERE NOT EXISTS (
SELECT 1 FROM runs run WHERE run.id=node.run_id))`,
	`SELECT EXISTS (SELECT 1 FROM run_events event WHERE NOT EXISTS (
SELECT 1 FROM runs run WHERE run.id=event.run_id))`,
	`SELECT EXISTS (SELECT 1 FROM workflow_draft_checkpoints checkpoint WHERE NOT EXISTS (
SELECT 1 FROM workflows workflow WHERE workflow.id=checkpoint.workflow_id))`,
	`SELECT EXISTS (SELECT 1 FROM workflow_draft_checkpoints checkpoint WHERE NOT EXISTS (
SELECT 1 FROM workflow_versions version WHERE version.id=checkpoint.restored_from_version_id AND version.workflow_id=checkpoint.workflow_id))`,
}

func wrapRestoreFailure(operation string, err error) error {
	if err == nil {
		return Wrap(CodeRestoreFailed, operation, nil)
	}
	if CodeOf(err) != "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return Wrap(CodeRestoreFailed, operation, nil)
}
