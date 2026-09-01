package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

const runSelectColumns = `id::text,workflow_id::text,workflow_version_id::text,draft_revision,
    graph_snapshot,source_run_id::text,source_node_id,retry_of_run_id::text,retry_key::text,
    agent_request_key::text,mode,status,execution_protocol,lease_owner,lease_token,lease_expires_at,
    recovery_reason,recovery_requested_at,input,input_redacted_paths,output,error,cancel_requested_at,heartbeat_at,started_at,ended_at`

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
		retry_of_run_id,retry_key,agent_request_key,mode,status,input,input_redacted_paths,output,error,cancel_requested_at,heartbeat_at,started_at,ended_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		run.ID, run.WorkflowID, run.WorkflowVersionID, run.DraftRevision, nullableRaw(run.GraphSnapshot),
		run.SourceRunID, run.SourceNodeID, run.RetryOfRunID, run.RetryKey, run.AgentRequestKey, run.Mode, run.Status, run.Input, inputPaths,
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

func (store *Store) CreateRetryRun(ctx context.Context, run domain.Run) (string, error) {
	if run.RetryOfRunID == nil || run.RetryKey == nil || run.AgentRequestKey != nil {
		return "", workflowservice.ErrInvalidWorkflowInput
	}
	errorJSON, err := marshalOptional(run.Error)
	if err != nil {
		return "", fmt.Errorf("encode retry run error: %w", err)
	}
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin create retry run: %w", err)
	}
	defer transaction.Rollback(ctx)

	var sourceWorkflowID string
	var sourceMode domain.RunMode
	var sourceStatus domain.RunStatus
	if err := transaction.QueryRow(ctx, `SELECT workflow_id::text,mode,status FROM runs WHERE id=$1 FOR UPDATE`, *run.RetryOfRunID).
		Scan(&sourceWorkflowID, &sourceMode, &sourceStatus); err != nil {
		return "", fmt.Errorf("lock retry source run: %w", mapNotFound(err))
	}
	if sourceWorkflowID != run.WorkflowID || sourceMode != run.Mode || (sourceStatus != domain.RunFailed && sourceStatus != domain.RunCancelled) {
		return "", workflowservice.ErrRunNotRetryable
	}
	var archivedAt *time.Time
	if err := transaction.QueryRow(ctx, "SELECT archived_at FROM workflows WHERE id=$1 FOR UPDATE", sourceWorkflowID).Scan(&archivedAt); err != nil {
		return "", fmt.Errorf("lock retry workflow: %w", mapNotFound(err))
	}
	if archivedAt != nil {
		return "", domain.ErrWorkflowArchived
	}
	inputPaths := run.InputRedactedPaths
	if inputPaths == nil {
		inputPaths = []string{}
	}
	_, err = transaction.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,source_run_id,source_node_id,
		retry_of_run_id,retry_key,agent_request_key,mode,status,input,input_redacted_paths,output,error,cancel_requested_at,heartbeat_at,started_at,ended_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		run.ID, run.WorkflowID, run.WorkflowVersionID, run.DraftRevision, nullableRaw(run.GraphSnapshot),
		run.SourceRunID, run.SourceNodeID, run.RetryOfRunID, run.RetryKey, run.AgentRequestKey, run.Mode, run.Status, run.Input, inputPaths,
		nullableRaw(run.Output), errorJSON, run.CancelRequestedAt, run.HeartbeatAt, run.StartedAt, run.EndedAt,
	)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" && databaseError.ConstraintName == "runs_retry_key_unique_idx" {
			if rollbackErr := transaction.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return "", fmt.Errorf("rollback duplicate retry run: %w", rollbackErr)
			}
			var existingID string
			if queryErr := store.pool.QueryRow(ctx, `SELECT id::text FROM runs WHERE retry_of_run_id=$1 AND retry_key=$2`, *run.RetryOfRunID, *run.RetryKey).Scan(&existingID); queryErr != nil {
				return "", fmt.Errorf("load existing retry run: %w", queryErr)
			}
			return "", &workflowservice.RunRetryAlreadyCreatedError{RunID: existingID}
		}
		return "", fmt.Errorf("create retry run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit create retry run: %w", err)
	}
	return run.ID, nil
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
	if nodeRun.Attempt == 0 {
		nodeRun.Attempt = 1
	}
	_, err = executor.Exec(ctx, `INSERT INTO node_runs(
        id,run_id,node_id,node_type,attempt,status,input,output,error,started_at,ended_at
    ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
    ON CONFLICT(run_id,node_id) DO UPDATE SET
		node_type=EXCLUDED.node_type,attempt=EXCLUDED.attempt,status=EXCLUDED.status,input=EXCLUDED.input,
        output=EXCLUDED.output,error=EXCLUDED.error,started_at=EXCLUDED.started_at,ended_at=EXCLUDED.ended_at`,
		nodeRun.ID, nodeRun.RunID, nodeRun.NodeID, nodeRun.NodeType, nodeRun.Attempt, nodeRun.Status,
		nullableRaw(nodeRun.Input), nullableRaw(nodeRun.Output), errorJSON, nodeRun.StartedAt, nodeRun.EndedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert node run: %w", err)
	}
	return nil
}

func (store *Store) PersistRunEvent(ctx context.Context, event domain.RunEvent, nodeRun *domain.NodeRun, budget domain.RunEventBudget) error {
	ensureNodeAttempt(&event, nodeRun)
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
	var executionProtocol int16
	if err := transaction.QueryRow(ctx, "SELECT id::text,execution_protocol FROM runs WHERE id=$1 FOR UPDATE", event.RunID).Scan(&runID, &executionProtocol); err != nil {
		return fmt.Errorf("lock run for event: %w", mapNotFound(err))
	}
	if executionProtocol != 0 {
		return domain.ErrRunLeaseLost
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
		run_id,sequence,type,node_id,node_attempt,status,input,output,active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		event.RunID, event.Sequence, event.Type, nullableString(event.NodeID), event.NodeAttempt, nullableNodeStatus(event.Status),
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
	rows, err := store.pool.Query(ctx, `SELECT run_id::text,sequence,type,node_id,node_attempt,status,input,output,
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
		&event.RunID, &event.Sequence, &event.Type, &nodeID, &event.NodeAttempt, &status, &input, &output,
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

func (store *Store) FinalizeRun(ctx context.Context, finalization workflowservice.RunFinalization) (domain.RunEvent, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("begin finalize run: %w", err)
	}
	defer transaction.Rollback(ctx)
	event, err := store.finalizeRunTx(ctx, transaction, finalization, false)
	if err != nil {
		return domain.RunEvent{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.RunEvent{}, fmt.Errorf("commit run finalization: %w", err)
	}
	return event, nil
}

func (store *Store) finalizeRunTx(ctx context.Context, transaction pgx.Tx, finalization workflowservice.RunFinalization, durableLeaseVerified bool) (domain.RunEvent, error) {
	var status domain.RunStatus
	var cancelRequestedAt *time.Time
	var executionProtocol int16
	if err := transaction.QueryRow(ctx, "SELECT status,cancel_requested_at,execution_protocol FROM runs WHERE id=$1 FOR UPDATE", finalization.RunID).Scan(&status, &cancelRequestedAt, &executionProtocol); err != nil {
		return domain.RunEvent{}, fmt.Errorf("lock run for finalization: %w", mapNotFound(err))
	}
	if executionProtocol != 0 && !durableLeaseVerified {
		return domain.RunEvent{}, domain.ErrRunLeaseLost
	}
	if isTerminalRunStatus(status) {
		event, err := existingTerminalEvent(ctx, transaction, finalization.RunID, status)
		if err != nil {
			return domain.RunEvent{}, err
		}
		return event, nil
	}
	inactiveCancellation := durableLeaseVerified && (status == domain.RunQueued || status == domain.RunRecoveryRequired) && finalization.Status == domain.RunCancelled
	if status != domain.RunRunning && status != domain.RunCancelling && !inactiveCancellation {
		return domain.RunEvent{}, fmt.Errorf("invalid active run status %q", status)
	}
	terminal := cloneStoredRunEvent(finalization.TerminalEvent)
	chosenStatus := finalization.Status
	output := finalization.Output
	publicError := finalization.Error
	cancellationWins := status == domain.RunCancelling || cancelRequestedAt != nil
	if cancellationWins {
		chosenStatus = domain.RunCancelled
		if publicError == nil || publicError.Code != "RUN_INTERRUPTED" {
			publicError = domain.NewPublicRunError(context.Canceled)
		}
	}
	if chosenStatus == domain.RunCancelled {
		output = nil
		if publicError == nil {
			publicError = domain.NewPublicRunError(context.Canceled)
		}
		terminal.Type = "run.cancelled"
		terminal.Output = nil
		terminal.Error = publicError
	}
	if !isTerminalRunStatus(chosenStatus) || terminal.Type != terminalEventType(chosenStatus) {
		return domain.RunEvent{}, fmt.Errorf("terminal event %q does not match status %q", terminal.Type, chosenStatus)
	}
	terminal.RunID = finalization.RunID
	terminal.Timestamp = finalization.EndedAt
	terminal.ActivePorts = nonNilStrings(terminal.ActivePorts)
	terminal.InputRedactedPaths = nonNilStrings(terminal.InputRedactedPaths)
	terminal.OutputRedactedPaths = nonNilStrings(terminal.OutputRedactedPaths)
	terminalErrorJSON, err := marshalOptional(terminal.Error)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("encode terminal error: %w", err)
	}
	activePortsJSON, _ := json.Marshal(terminal.ActivePorts)
	inputPathsJSON, _ := json.Marshal(terminal.InputRedactedPaths)
	outputPathsJSON, _ := json.Marshal(terminal.OutputRedactedPaths)
	terminal.DataBytes = int64(len(terminal.Input) + len(terminal.Output) + len(terminalErrorJSON) + len(activePortsJSON) + len(inputPathsJSON) + len(outputPathsJSON))
	var count int
	var maxSequence, publicDataBytes, privateDataBytes int64
	if err := transaction.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM run_events WHERE run_id=$1),
		(SELECT COALESCE(max(sequence),0) FROM run_events WHERE run_id=$1),
		(SELECT COALESCE(sum(data_bytes),0) FROM run_events WHERE run_id=$1),
		(SELECT COALESCE(sum(octet_length(ciphertext)),0) FROM run_payloads WHERE run_id=$1)`, finalization.RunID).Scan(&count, &maxSequence, &publicDataBytes, &privateDataBytes); err != nil {
		return domain.RunEvent{}, fmt.Errorf("read terminal event budget: %w", err)
	}
	if terminal.Sequence != maxSequence+1 {
		return domain.RunEvent{}, fmt.Errorf("%w: sequence %d follows %d", domain.ErrRunEventSequence, terminal.Sequence, maxSequence)
	}
	totalDataBytes := publicDataBytes
	if durableLeaseVerified {
		totalDataBytes += privateDataBytes
	}
	if count >= finalization.Budget.MaxEvents || terminal.DataBytes < 0 || terminal.DataBytes > finalization.Budget.MaxTotalDataBytes-totalDataBytes {
		return domain.RunEvent{}, fmt.Errorf("%w: events=%d bytes=%d", domain.ErrRunEventBudgetExceeded, count, totalDataBytes)
	}
	outputJSON, err := marshalOptional(output)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("encode run output: %w", err)
	}
	errorJSON, err := marshalOptional(publicError)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("encode run error: %w", err)
	}
	if _, err := transaction.Exec(ctx, `UPDATE runs SET status=$2,output=$3,error=$4,ended_at=$5,
		lease_owner=NULL,lease_expires_at=NULL,recovery_reason=NULL,recovery_requested_at=NULL WHERE id=$1`,
		finalization.RunID, chosenStatus, outputJSON, errorJSON, finalization.EndedAt); err != nil {
		return domain.RunEvent{}, fmt.Errorf("update finalized run: %w", err)
	}
	if chosenStatus == domain.RunCancelled {
		if _, err := transaction.Exec(ctx, `UPDATE node_runs SET status='cancelled',ended_at=COALESCE(ended_at,$2)
			WHERE run_id=$1 AND status='running'`, finalization.RunID, finalization.EndedAt); err != nil {
			return domain.RunEvent{}, fmt.Errorf("cancel active node runs: %w", err)
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO run_events(
		run_id,sequence,type,node_id,node_attempt,status,input,output,active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		terminal.RunID, terminal.Sequence, terminal.Type, nullableString(terminal.NodeID), terminal.NodeAttempt, nullableNodeStatus(terminal.Status),
		nullableRaw(terminal.Input), nullableRaw(terminal.Output), terminal.ActivePorts, terminalErrorJSON,
		terminal.InputRedactedPaths, terminal.OutputRedactedPaths, terminal.DataBytes, terminal.Timestamp,
	); err != nil {
		return domain.RunEvent{}, fmt.Errorf("insert terminal run event: %w", err)
	}
	return terminal, nil
}

func existingTerminalEvent(ctx context.Context, transaction pgx.Tx, runID string, status domain.RunStatus) (domain.RunEvent, error) {
	var count int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM run_events
		WHERE run_id=$1 AND type IN ('run.completed','run.failed','run.cancelled')`, runID).Scan(&count); err != nil {
		return domain.RunEvent{}, fmt.Errorf("count existing terminal events: %w", err)
	}
	if count != 1 {
		return domain.RunEvent{}, fmt.Errorf("finalized run has %d terminal events", count)
	}
	event, err := scanRunEvent(transaction.QueryRow(ctx, `SELECT run_id::text,sequence,type,node_id,node_attempt,status,input,output,
		active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
		FROM run_events WHERE run_id=$1 ORDER BY sequence DESC LIMIT 1`, runID))
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("read existing terminal event: %w", err)
	}
	if event.Type != terminalEventType(status) {
		return domain.RunEvent{}, fmt.Errorf("finalized run status %q mismatches terminal event %q", status, event.Type)
	}
	return event, nil
}

func isTerminalRunStatus(status domain.RunStatus) bool {
	return domain.IsTerminalRunStatus(status)
}

func terminalEventType(status domain.RunStatus) string {
	switch status {
	case domain.RunCompleted:
		return "run.completed"
	case domain.RunFailed:
		return "run.failed"
	case domain.RunCancelled:
		return "run.cancelled"
	default:
		return ""
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string{}, values...)
}

func cloneStoredRunEvent(event domain.RunEvent) domain.RunEvent {
	cloned := event
	cloned.Input = append(json.RawMessage(nil), event.Input...)
	cloned.Output = append(json.RawMessage(nil), event.Output...)
	cloned.ActivePorts = nonNilStrings(event.ActivePorts)
	cloned.InputRedactedPaths = nonNilStrings(event.InputRedactedPaths)
	cloned.OutputRedactedPaths = nonNilStrings(event.OutputRedactedPaths)
	if event.Error != nil {
		errorCopy := *event.Error
		cloned.Error = &errorCopy
	}
	return cloned
}

func (store *Store) GetRun(ctx context.Context, runID string) (domain.Run, []domain.NodeRun, error) {
	run, err := scanRun(store.pool.QueryRow(ctx, `SELECT `+runSelectColumns+` FROM runs WHERE id=$1`, runID))
	if err != nil {
		return domain.Run{}, nil, mapNotFound(err)
	}
	rows, err := store.pool.Query(ctx, `SELECT id::text,run_id::text,node_id,node_type,attempt,status,input,output,error,started_at,ended_at
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
	var leaseOwner, recoveryReason *string
	var graphSnapshot, input, output, errorJSON []byte
	if err := row.Scan(
		&run.ID, &run.WorkflowID, &run.WorkflowVersionID, &run.DraftRevision,
		&graphSnapshot, &run.SourceRunID, &run.SourceNodeID, &run.RetryOfRunID, &run.RetryKey,
		&run.AgentRequestKey, &run.Mode, &run.Status, &run.ExecutionProtocol, &leaseOwner, &run.LeaseToken, &run.LeaseExpiresAt,
		&recoveryReason, &run.RecoveryRequestedAt, &input, &run.InputRedactedPaths, &output, &errorJSON,
		&run.CancelRequestedAt, &run.HeartbeatAt,
		&run.StartedAt, &run.EndedAt,
	); err != nil {
		return domain.Run{}, err
	}
	run.GraphSnapshot = json.RawMessage(graphSnapshot)
	run.Input = json.RawMessage(input)
	run.Output = json.RawMessage(output)
	if leaseOwner != nil {
		run.LeaseOwner = *leaseOwner
	}
	if recoveryReason != nil {
		run.RecoveryReason = domain.RunRecoveryReason(*recoveryReason)
	}
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
		&nodeRun.ID, &nodeRun.RunID, &nodeRun.NodeID, &nodeRun.NodeType, &nodeRun.Attempt, &nodeRun.Status,
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

func ensureNodeAttempt(event *domain.RunEvent, nodeRun *domain.NodeRun) {
	if event.NodeID != "" && event.NodeAttempt == nil {
		attempt := 1
		if nodeRun != nil && nodeRun.Attempt > 0 {
			attempt = nodeRun.Attempt
		}
		event.NodeAttempt = &attempt
	}
	if nodeRun != nil && nodeRun.Attempt == 0 {
		nodeRun.Attempt = 1
	}
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
