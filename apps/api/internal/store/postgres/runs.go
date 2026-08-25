package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const runSelectColumns = `id::text,workflow_id::text,workflow_version_id::text,draft_revision,
    graph_snapshot,source_run_id::text,source_node_id,retry_of_run_id::text,retry_key::text,
    mode,status,input,input_redacted_paths,output,error,cancel_requested_at,heartbeat_at,started_at,ended_at`

func (store *Store) CreateRun(ctx context.Context, run domain.Run) error {
	errorJSON, err := marshalOptional(run.Error)
	if err != nil {
		return fmt.Errorf("encode run error: %w", err)
	}
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin create run: %w", err)
	}
	defer transaction.Rollback(ctx)
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT archived_at FROM workflows WHERE id=$1 FOR SHARE", run.WorkflowID).Scan(&archivedAt); err != nil {
		return mapNotFound(err)
	}
	if archivedAt != nil {
		return domain.ErrWorkflowArchived
	}
	inputPaths := run.InputRedactedPaths
	if inputPaths == nil {
		inputPaths = []string{}
	}
	_, err = transaction.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,source_run_id,source_node_id,
		retry_of_run_id,retry_key,mode,status,input,input_redacted_paths,output,error,cancel_requested_at,heartbeat_at,started_at,ended_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		run.ID, run.WorkflowID, run.WorkflowVersionID, run.DraftRevision, nullableRaw(run.GraphSnapshot),
		run.SourceRunID, run.SourceNodeID, run.RetryOfRunID, run.RetryKey, run.Mode, run.Status, run.Input, inputPaths,
		nullableRaw(run.Output), errorJSON, run.CancelRequestedAt, run.HeartbeatAt, run.StartedAt, run.EndedAt,
	)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit create run: %w", err)
	}
	return nil
}

func (store *Store) UpsertNodeRun(ctx context.Context, nodeRun domain.NodeRun) error {
	return upsertNodeRun(ctx, store.pool, nodeRun)
}

type runExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func upsertNodeRun(ctx context.Context, executor runExecer, nodeRun domain.NodeRun) error {
	errorJSON, err := marshalOptional(nodeRun.Error)
	if err != nil {
		return fmt.Errorf("encode node run error: %w", err)
	}
	_, err = executor.Exec(ctx, `INSERT INTO node_runs(
        id,run_id,node_id,node_type,status,input,output,error,started_at,ended_at
    ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    ON CONFLICT(run_id,node_id) DO UPDATE SET
        node_type=EXCLUDED.node_type,status=EXCLUDED.status,input=EXCLUDED.input,
        output=EXCLUDED.output,error=EXCLUDED.error,started_at=EXCLUDED.started_at,ended_at=EXCLUDED.ended_at`,
		nodeRun.ID, nodeRun.RunID, nodeRun.NodeID, nodeRun.NodeType, nodeRun.Status,
		nullableRaw(nodeRun.Input), nullableRaw(nodeRun.Output), errorJSON, nodeRun.StartedAt, nodeRun.EndedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert node run: %w", err)
	}
	return nil
}

func (store *Store) PersistRunEvent(ctx context.Context, event domain.RunEvent, nodeRun *domain.NodeRun, budget domain.RunEventBudget) error {
	errorJSON, err := marshalOptional(event.Error)
	if err != nil {
		return fmt.Errorf("encode run event error: %w", err)
	}
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin run event: %w", err)
	}
	defer transaction.Rollback(ctx)
	var runID string
	if err := transaction.QueryRow(ctx, "SELECT id::text FROM runs WHERE id=$1 FOR UPDATE", event.RunID).Scan(&runID); err != nil {
		return fmt.Errorf("lock run for event: %w", mapNotFound(err))
	}
	var count int
	var maxSequence, totalDataBytes int64
	if err := transaction.QueryRow(ctx, `SELECT count(*),COALESCE(max(sequence),0),COALESCE(sum(data_bytes),0)
		FROM run_events WHERE run_id=$1`, event.RunID).Scan(&count, &maxSequence, &totalDataBytes); err != nil {
		return fmt.Errorf("read run event budget: %w", err)
	}
	if event.Sequence != maxSequence+1 {
		return fmt.Errorf("%w: sequence %d follows %d", domain.ErrRunEventSequence, event.Sequence, maxSequence)
	}
	if count >= budget.MaxEvents || event.DataBytes < 0 || event.DataBytes > budget.MaxTotalDataBytes-totalDataBytes {
		return fmt.Errorf("%w: events=%d bytes=%d", domain.ErrRunEventBudgetExceeded, count, totalDataBytes)
	}
	activePorts := event.ActivePorts
	if activePorts == nil {
		activePorts = []string{}
	}
	inputPaths := event.InputRedactedPaths
	if inputPaths == nil {
		inputPaths = []string{}
	}
	outputPaths := event.OutputRedactedPaths
	if outputPaths == nil {
		outputPaths = []string{}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO run_events(
		run_id,sequence,type,node_id,status,input,output,active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		event.RunID, event.Sequence, event.Type, nullableString(event.NodeID), nullableNodeStatus(event.Status),
		nullableRaw(event.Input), nullableRaw(event.Output), activePorts, errorJSON, inputPaths, outputPaths, event.DataBytes, event.Timestamp,
	); err != nil {
		return fmt.Errorf("insert run event: %w", err)
	}
	if nodeRun != nil {
		if err := upsertNodeRun(ctx, transaction, *nodeRun); err != nil {
			return err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit run event: %w", err)
	}
	return nil
}

func (store *Store) ListRunEvents(ctx context.Context, runID string, afterSequence int64, limit int) ([]domain.RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := store.pool.Query(ctx, `SELECT run_id::text,sequence,type,node_id,status,input,output,
		active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
		FROM run_events WHERE run_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, runID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.RunEvent, 0)
	for rows.Next() {
		event, err := scanRunEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	return events, nil
}

func scanRunEvent(row runScanner) (domain.RunEvent, error) {
	var event domain.RunEvent
	var nodeID, status *string
	var input, output, errorJSON []byte
	if err := row.Scan(
		&event.RunID, &event.Sequence, &event.Type, &nodeID, &status, &input, &output,
		&event.ActivePorts, &errorJSON, &event.InputRedactedPaths, &event.OutputRedactedPaths, &event.DataBytes, &event.Timestamp,
	); err != nil {
		return domain.RunEvent{}, err
	}
	if nodeID != nil {
		event.NodeID = *nodeID
	}
	if status != nil {
		event.Status = domain.NodeStatus(*status)
	}
	event.Input = json.RawMessage(input)
	event.Output = json.RawMessage(output)
	if event.ActivePorts == nil {
		event.ActivePorts = []string{}
	}
	if event.InputRedactedPaths == nil {
		event.InputRedactedPaths = []string{}
	}
	if event.OutputRedactedPaths == nil {
		event.OutputRedactedPaths = []string{}
	}
	if len(errorJSON) > 0 {
		if err := json.Unmarshal(errorJSON, &event.Error); err != nil {
			return domain.RunEvent{}, fmt.Errorf("decode run event error: %w", err)
		}
	}
	return event, nil
}

func (store *Store) FinishRun(ctx context.Context, runID string, status domain.RunStatus, output any, publicError *domain.PublicError, endedAt time.Time) error {
	outputJSON, err := marshalOptional(output)
	if err != nil {
		return fmt.Errorf("encode run output: %w", err)
	}
	errorJSON, err := marshalOptional(publicError)
	if err != nil {
		return fmt.Errorf("encode run error: %w", err)
	}
	command, err := store.pool.Exec(ctx, `UPDATE runs SET status=$2,output=$3,error=$4,ended_at=$5 WHERE id=$1`, runID, status, outputJSON, errorJSON, endedAt)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *Store) GetRun(ctx context.Context, runID string) (domain.Run, []domain.NodeRun, error) {
	run, err := scanRun(store.pool.QueryRow(ctx, `SELECT `+runSelectColumns+` FROM runs WHERE id=$1`, runID))
	if err != nil {
		return domain.Run{}, nil, mapNotFound(err)
	}
	rows, err := store.pool.Query(ctx, `SELECT id::text,run_id::text,node_id,node_type,status,input,output,error,started_at,ended_at
        FROM node_runs WHERE run_id=$1 ORDER BY node_id`, runID)
	if err != nil {
		return domain.Run{}, nil, fmt.Errorf("list node runs: %w", err)
	}
	defer rows.Close()
	nodeRuns := make([]domain.NodeRun, 0)
	for rows.Next() {
		nodeRun, err := scanNodeRun(rows)
		if err != nil {
			return domain.Run{}, nil, fmt.Errorf("scan node run: %w", err)
		}
		nodeRuns = append(nodeRuns, nodeRun)
	}
	if err := rows.Err(); err != nil {
		return domain.Run{}, nil, fmt.Errorf("list node runs: %w", err)
	}
	return run, nodeRuns, nil
}

func (store *Store) ListRuns(ctx context.Context, workflowID string, limit int) ([]domain.Run, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := store.pool.Query(ctx, `SELECT `+runSelectColumns+`
        FROM runs WHERE workflow_id=$1 ORDER BY started_at DESC,id LIMIT $2`, workflowID, limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.Run, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return runs, nil
}

type runScanner interface {
	Scan(...any) error
}

func scanRun(row runScanner) (domain.Run, error) {
	var run domain.Run
	var graphSnapshot, input, output, errorJSON []byte
	if err := row.Scan(
		&run.ID, &run.WorkflowID, &run.WorkflowVersionID, &run.DraftRevision,
		&graphSnapshot, &run.SourceRunID, &run.SourceNodeID, &run.RetryOfRunID, &run.RetryKey,
		&run.Mode, &run.Status, &input, &run.InputRedactedPaths, &output, &errorJSON,
		&run.CancelRequestedAt, &run.HeartbeatAt,
		&run.StartedAt, &run.EndedAt,
	); err != nil {
		return domain.Run{}, err
	}
	run.GraphSnapshot = json.RawMessage(graphSnapshot)
	run.Input = json.RawMessage(input)
	run.Output = json.RawMessage(output)
	if run.InputRedactedPaths == nil {
		run.InputRedactedPaths = []string{}
	}
	if len(errorJSON) > 0 {
		if err := json.Unmarshal(errorJSON, &run.Error); err != nil {
			return domain.Run{}, fmt.Errorf("decode run error: %w", err)
		}
	}
	return run, nil
}

func scanNodeRun(row runScanner) (domain.NodeRun, error) {
	var nodeRun domain.NodeRun
	var input, output, errorJSON []byte
	if err := row.Scan(
		&nodeRun.ID, &nodeRun.RunID, &nodeRun.NodeID, &nodeRun.NodeType, &nodeRun.Status,
		&input, &output, &errorJSON, &nodeRun.StartedAt, &nodeRun.EndedAt,
	); err != nil {
		return domain.NodeRun{}, err
	}
	nodeRun.Input = json.RawMessage(input)
	nodeRun.Output = json.RawMessage(output)
	if len(errorJSON) > 0 {
		if err := json.Unmarshal(errorJSON, &nodeRun.Error); err != nil {
			return domain.NodeRun{}, fmt.Errorf("decode node run error: %w", err)
		}
	}
	return nodeRun, nil
}

func marshalOptional(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func nullableRaw(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableNodeStatus(value domain.NodeStatus) any {
	if value == "" {
		return nil
	}
	return value
}
