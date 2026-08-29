package backup

import (
	"context"
	"encoding/json"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReferenceCounts is the number of staged records for each backup table.
type ReferenceCounts struct {
	Workflows                uint64
	WorkflowVersions         uint64
	Runs                     uint64
	NodeRuns                 uint64
	RunEvents                uint64
	WorkflowDraftCheckpoints uint64
}

var referenceTableStatements = []string{
	`CREATE TEMP TABLE backup_workflow_refs(
  ordinal bigint PRIMARY KEY, id uuid UNIQUE NOT NULL, published_version_id uuid NULL
) ON COMMIT DROP`,
	`CREATE TEMP TABLE backup_version_refs(
  ordinal bigint PRIMARY KEY, id uuid UNIQUE NOT NULL, workflow_id uuid NOT NULL, version integer NOT NULL,
  UNIQUE(workflow_id, version), UNIQUE(workflow_id, id)
) ON COMMIT DROP`,
	`CREATE TEMP TABLE backup_run_refs(
  ordinal bigint PRIMARY KEY, id uuid UNIQUE NOT NULL, workflow_id uuid NOT NULL,
  workflow_version_id uuid NULL, source_run_id uuid NULL, retry_of_run_id uuid NULL
) ON COMMIT DROP`,
	`CREATE TEMP TABLE backup_node_run_refs(
  ordinal bigint PRIMARY KEY, id uuid UNIQUE NOT NULL, run_id uuid NOT NULL
) ON COMMIT DROP`,
	`CREATE TEMP TABLE backup_event_refs(
  ordinal bigint PRIMARY KEY, run_id uuid NOT NULL, sequence bigint NOT NULL, UNIQUE(run_id, sequence)
) ON COMMIT DROP`,
	`CREATE TEMP TABLE backup_checkpoint_refs(
  ordinal bigint PRIMARY KEY, workflow_id uuid UNIQUE NOT NULL, restored_from_version_id uuid NOT NULL
) ON COMMIT DROP`,
}

func stageReferences(ctx context.Context, tx pgx.Tx, archive *Archive) (ReferenceCounts, error) {
	if err := createReferenceTables(ctx, tx); err != nil {
		return ReferenceCounts{}, err
	}

	var counts ReferenceCounts
	for _, table := range TableOrder {
		count, err := copyReferenceTable(ctx, tx, archive, table)
		if err != nil {
			return ReferenceCounts{}, err
		}
		switch table {
		case TableWorkflows:
			counts.Workflows = count
		case TableWorkflowVersions:
			counts.WorkflowVersions = count
		case TableRuns:
			counts.Runs = count
		case TableNodeRuns:
			counts.NodeRuns = count
		case TableRunEvents:
			counts.RunEvents = count
		case TableWorkflowDraftCheckpoints:
			counts.WorkflowDraftCheckpoints = count
		}
	}
	if err := validateReferenceCounts(ctx, tx, archive, counts); err != nil {
		return ReferenceCounts{}, err
	}
	if err := validateReferenceRelationships(ctx, tx); err != nil {
		return ReferenceCounts{}, err
	}
	return counts, nil
}

func createReferenceTables(ctx context.Context, tx pgx.Tx) error {
	for _, statement := range referenceTableStatements {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return Wrap(CodeRestoreFailed, "create backup reference tables", nil)
		}
	}
	return nil
}

func copyReferenceTable(ctx context.Context, tx pgx.Tx, archive *Archive, table TableName) (uint64, error) {
	target, columns := referenceCopyTarget(table)
	if target == "" {
		return 0, Wrap(CodeArchiveInvalid, "select backup reference table", nil)
	}
	source := newReferenceCopySource(ctx, archive, table)
	count, err := tx.CopyFrom(ctx, pgx.Identifier{target}, columns, source)
	if err != nil {
		if CodeOf(err) != "" {
			return 0, err
		}
		return 0, Wrap(CodeReferenceInvalid, "stage backup references", nil)
	}
	return uint64(count), nil
}

func referenceCopyTarget(table TableName) (string, []string) {
	switch table {
	case TableWorkflows:
		return "backup_workflow_refs", []string{"ordinal", "id", "published_version_id"}
	case TableWorkflowVersions:
		return "backup_version_refs", []string{"ordinal", "id", "workflow_id", "version"}
	case TableRuns:
		return "backup_run_refs", []string{"ordinal", "id", "workflow_id", "workflow_version_id", "source_run_id", "retry_of_run_id"}
	case TableNodeRuns:
		return "backup_node_run_refs", []string{"ordinal", "id", "run_id"}
	case TableRunEvents:
		return "backup_event_refs", []string{"ordinal", "run_id", "sequence"}
	case TableWorkflowDraftCheckpoints:
		return "backup_checkpoint_refs", []string{"ordinal", "workflow_id", "restored_from_version_id"}
	default:
		return "", nil
	}
}

type referenceCopySource struct {
	ctx     context.Context
	items   chan referenceCopyItem
	values  []any
	err     error
	started bool
	archive *Archive
	table   TableName
}

type referenceCopyItem struct {
	values []any
	err    error
	done   bool
}

func newReferenceCopySource(ctx context.Context, archive *Archive, table TableName) *referenceCopySource {
	return &referenceCopySource{ctx: ctx, archive: archive, table: table, items: make(chan referenceCopyItem)}
}

func (source *referenceCopySource) Next() bool {
	if !source.started {
		source.started = true
		go source.stream()
	}
	select {
	case item := <-source.items:
		if item.done {
			source.err = item.err
			return false
		}
		if item.err != nil {
			source.err = item.err
			return false
		}
		source.values = item.values
		return true
	case <-source.ctx.Done():
		source.err = source.ctx.Err()
		return false
	}
}

func (source *referenceCopySource) Values() ([]any, error) { return source.values, nil }

func (source *referenceCopySource) Err() error { return source.err }

func (source *referenceCopySource) stream() {
	var ordinal int64
	err := source.archive.ReadTable(source.ctx, source.table, func(raw json.RawMessage) error {
		if ordinal == math.MaxInt64 {
			return Wrap(CodeArchiveInvalid, "validate backup reference ordinal", nil)
		}
		ordinal++
		values, err := referenceValues(source.table, ordinal, raw)
		if err != nil {
			return err
		}
		select {
		case source.items <- referenceCopyItem{values: values}:
			return nil
		case <-source.ctx.Done():
			return source.ctx.Err()
		}
	})
	select {
	case source.items <- referenceCopyItem{err: err, done: true}:
	case <-source.ctx.Done():
	}
}

func referenceValues(table TableName, ordinal int64, raw json.RawMessage) ([]any, error) {
	switch table {
	case TableWorkflows:
		record, err := decodeWorkflowRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.ID), optionalReferenceUUID(record.PublishedVersionID)}, nil
	case TableWorkflowVersions:
		record, err := decodeWorkflowVersionRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.ID), referenceUUID(record.WorkflowID), record.Version}, nil
	case TableRuns:
		record, err := decodeRunRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.ID), referenceUUID(record.WorkflowID), optionalReferenceUUID(record.WorkflowVersionID), optionalReferenceUUID(record.SourceRunID), optionalReferenceUUID(record.RetryOfRunID)}, nil
	case TableNodeRuns:
		record, err := decodeNodeRunRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.ID), referenceUUID(record.RunID)}, nil
	case TableRunEvents:
		record, err := decodeRunEventRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.RunID), record.Sequence}, nil
	case TableWorkflowDraftCheckpoints:
		record, err := decodeWorkflowDraftCheckpointRecord(raw)
		if err != nil {
			return nil, err
		}
		return []any{ordinal, referenceUUID(record.WorkflowID), referenceUUID(record.RestoredFromVersionID)}, nil
	default:
		return nil, Wrap(CodeArchiveInvalid, "select backup record type", nil)
	}
}

func referenceUUID(value string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(value), Valid: true}
}

func optionalReferenceUUID(value *string) any {
	if value == nil {
		return nil
	}
	return referenceUUID(*value)
}

func validateReferenceCounts(ctx context.Context, tx pgx.Tx, archive *Archive, counts ReferenceCounts) error {
	for _, item := range []struct {
		table string
		want  uint64
		got   uint64
	}{
		{"backup_workflow_refs", archive.table(TableWorkflows).Records, counts.Workflows},
		{"backup_version_refs", archive.table(TableWorkflowVersions).Records, counts.WorkflowVersions},
		{"backup_run_refs", archive.table(TableRuns).Records, counts.Runs},
		{"backup_node_run_refs", archive.table(TableNodeRuns).Records, counts.NodeRuns},
		{"backup_event_refs", archive.table(TableRunEvents).Records, counts.RunEvents},
		{"backup_checkpoint_refs", archive.table(TableWorkflowDraftCheckpoints).Records, counts.WorkflowDraftCheckpoints},
	} {
		var actual uint64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+item.table).Scan(&actual); err != nil {
			return Wrap(CodeRestoreFailed, "count backup references", nil)
		}
		if item.want != item.got || item.want != actual {
			return Wrap(CodeReferenceInvalid, "validate backup reference counts", nil)
		}
	}
	return nil
}

func validateReferenceRelationships(ctx context.Context, tx pgx.Tx) error {
	queries := []string{
		`SELECT EXISTS (SELECT 1 FROM backup_version_refs version WHERE NOT EXISTS (
  SELECT 1 FROM backup_workflow_refs workflow WHERE workflow.id=version.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_workflow_refs workflow WHERE workflow.published_version_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM backup_version_refs version WHERE version.id=workflow.published_version_id AND version.workflow_id=workflow.id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_run_refs run WHERE NOT EXISTS (
  SELECT 1 FROM backup_workflow_refs workflow WHERE workflow.id=run.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_run_refs run WHERE run.workflow_version_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM backup_version_refs version WHERE version.id=run.workflow_version_id AND version.workflow_id=run.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_run_refs child WHERE child.source_run_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM backup_run_refs parent WHERE parent.id=child.source_run_id AND parent.workflow_id=child.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_run_refs child WHERE child.retry_of_run_id IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM backup_run_refs parent WHERE parent.id=child.retry_of_run_id AND parent.workflow_id=child.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_run_refs child JOIN backup_run_refs parent
  ON parent.id=child.source_run_id OR parent.id=child.retry_of_run_id WHERE parent.ordinal >= child.ordinal)`,
		`SELECT EXISTS (SELECT 1 FROM backup_node_run_refs node_run WHERE NOT EXISTS (
  SELECT 1 FROM backup_run_refs run WHERE run.id=node_run.run_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_event_refs event WHERE NOT EXISTS (
  SELECT 1 FROM backup_run_refs run WHERE run.id=event.run_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_checkpoint_refs checkpoint WHERE NOT EXISTS (
  SELECT 1 FROM backup_workflow_refs workflow WHERE workflow.id=checkpoint.workflow_id))`,
		`SELECT EXISTS (SELECT 1 FROM backup_checkpoint_refs checkpoint WHERE NOT EXISTS (
  SELECT 1 FROM backup_version_refs version WHERE version.id=checkpoint.restored_from_version_id AND version.workflow_id=checkpoint.workflow_id))`,
	}
	for _, query := range queries {
		invalid, err := referenceQueryExists(ctx, tx, query)
		if err != nil {
			return err
		}
		if invalid {
			return Wrap(CodeReferenceInvalid, "validate backup references", nil)
		}
	}
	return validateRunReferenceCycles(ctx, tx)
}

func validateRunReferenceCycles(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE backup_run_remaining ON COMMIT DROP AS TABLE backup_run_refs"); err != nil {
		return Wrap(CodeRestoreFailed, "create backup run reference graph", nil)
	}
	for {
		var remaining int64
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM backup_run_remaining").Scan(&remaining); err != nil {
			return Wrap(CodeRestoreFailed, "count backup run reference graph", nil)
		}
		if remaining == 0 {
			return nil
		}
		tag, err := tx.Exec(ctx, `DELETE FROM backup_run_remaining child
WHERE NOT EXISTS (
  SELECT 1 FROM backup_run_remaining parent
  WHERE parent.id=child.source_run_id OR parent.id=child.retry_of_run_id
)`)
		if err != nil {
			return Wrap(CodeRestoreFailed, "reduce backup run reference graph", nil)
		}
		if tag.RowsAffected() == 0 {
			return Wrap(CodeReferenceInvalid, "validate backup run reference graph", nil)
		}
	}
}

func referenceQueryExists(ctx context.Context, tx pgx.Tx, query string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, query).Scan(&exists); err != nil {
		return false, Wrap(CodeRestoreFailed, "validate backup references", nil)
	}
	return exists, nil
}
