package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) SubmitRun(ctx context.Context, submission workflowservice.RunSubmission) error {
	run := submission.Run
	if run.Status != domain.RunQueued || run.ExecutionProtocol != domain.CurrentExecutionProtocol ||
		submission.QueuedEvent.RunID != run.ID || submission.QueuedEvent.Sequence != 1 || submission.QueuedEvent.Type != "run.queued" ||
		submission.InputPayload.RunID != run.ID || submission.InputPayload.Sequence != 0 || submission.InputPayload.Kind != domain.RunPayloadInput ||
		submission.InputPayload.ExecutionProtocol != run.ExecutionProtocol {
		return errors.New("invalid durable run submission")
	}
	errorJSON, err := marshalOptional(run.Error)
	if err != nil {
		return fmt.Errorf("encode submitted run error: %w", err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin durable run submission: %w", err)
	}
	defer tx.Rollback(ctx)
	var archivedAt *time.Time
	if err := tx.QueryRow(ctx, "SELECT archived_at FROM workflows WHERE id=$1 FOR SHARE", run.WorkflowID).Scan(&archivedAt); err != nil {
		return fmt.Errorf("lock submitted run workflow: %w", mapNotFound(err))
	}
	if archivedAt != nil {
		return domain.ErrWorkflowArchived
	}
	inputPaths := nonNilStrings(run.InputRedactedPaths)
	if _, err := tx.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,source_run_id,source_node_id,
		retry_of_run_id,retry_key,agent_request_key,mode,status,execution_protocol,input,input_redacted_paths,
		output,error,cancel_requested_at,heartbeat_at,started_at,ended_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		run.ID, run.WorkflowID, run.WorkflowVersionID, run.DraftRevision, nullableRaw(run.GraphSnapshot),
		run.SourceRunID, run.SourceNodeID, run.RetryOfRunID, run.RetryKey, run.AgentRequestKey, run.Mode, run.Status,
		run.ExecutionProtocol, run.Input, inputPaths, nullableRaw(run.Output), errorJSON, run.CancelRequestedAt,
		run.HeartbeatAt, run.StartedAt, run.EndedAt,
	); err != nil {
		return fmt.Errorf("insert durable run: %w", err)
	}
	event := cloneStoredRunEvent(submission.QueuedEvent)
	if err := insertRunEventRecord(ctx, tx, event); err != nil {
		return err
	}
	if err := insertRunPayloads(ctx, tx, []domain.RunPayload{submission.InputPayload}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit durable run submission: %w", err)
	}
	return nil
}

func (store *Store) ClaimRun(ctx context.Context, owner string, duration time.Duration) (workflowservice.ClaimedRun, bool, error) {
	if owner == "" || duration <= 0 {
		return workflowservice.ClaimedRun{}, false, errors.New("invalid run lease claim")
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return workflowservice.ClaimedRun{}, false, fmt.Errorf("begin run claim: %w", err)
	}
	defer tx.Rollback(ctx)
	var runID string
	err = tx.QueryRow(ctx, `SELECT id::text
		FROM runs
		WHERE (
			execution_protocol=$1
			AND (
				status='queued'
				OR (status='running' AND (lease_owner IS NULL OR lease_expires_at < clock_timestamp()))
			)
		) OR (
			status='cancelling'
			AND (lease_owner IS NULL OR lease_expires_at < clock_timestamp())
		)
		ORDER BY started_at,id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, domain.CurrentExecutionProtocol).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return workflowservice.ClaimedRun{}, false, nil
	}
	if err != nil {
		return workflowservice.ClaimedRun{}, false, fmt.Errorf("select claimable run: %w", err)
	}
	var token int64
	var expiresAt time.Time
	if err := tx.QueryRow(ctx, `UPDATE runs
		SET status=CASE WHEN status='queued' THEN 'running' ELSE status END,
			lease_owner=$2,lease_token=lease_token+1,
			lease_expires_at=clock_timestamp()+make_interval(secs=>$3),heartbeat_at=clock_timestamp()
		WHERE id=$1
		RETURNING lease_token,lease_expires_at`, runID, owner, duration.Seconds()).Scan(&token, &expiresAt); err != nil {
		return workflowservice.ClaimedRun{}, false, fmt.Errorf("claim run lease: %w", err)
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runSelectColumns+` FROM runs WHERE id=$1`, runID))
	if err != nil {
		return workflowservice.ClaimedRun{}, false, fmt.Errorf("load claimed run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return workflowservice.ClaimedRun{}, false, fmt.Errorf("commit run claim: %w", err)
	}
	lease := domain.RunLease{RunID: runID, Owner: owner, Token: token, ExpiresAt: expiresAt}
	return workflowservice.ClaimedRun{Run: run, Lease: lease}, true, nil
}

func (store *Store) RenewRunLease(ctx context.Context, lease domain.RunLease, duration time.Duration) (workflowservice.LeaseHeartbeat, error) {
	if lease.RunID == "" || lease.Owner == "" || lease.Token <= 0 || duration <= 0 {
		return workflowservice.LeaseHeartbeat{}, domain.ErrRunLeaseLost
	}
	var expiresAt time.Time
	var cancelRequestedAt *time.Time
	err := store.pool.QueryRow(ctx, `UPDATE runs
		SET lease_expires_at=clock_timestamp()+make_interval(secs=>$4),heartbeat_at=clock_timestamp()
		WHERE id=$1 AND lease_owner=$2 AND lease_token=$3
			AND lease_expires_at>=clock_timestamp() AND status IN ('running','cancelling')
		RETURNING lease_expires_at,cancel_requested_at`, lease.RunID, lease.Owner, lease.Token, duration.Seconds()).Scan(&expiresAt, &cancelRequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return workflowservice.LeaseHeartbeat{}, domain.ErrRunLeaseLost
	}
	if err != nil {
		return workflowservice.LeaseHeartbeat{}, fmt.Errorf("renew run lease: %w", err)
	}
	lease.ExpiresAt = expiresAt
	return workflowservice.LeaseHeartbeat{Lease: lease, CancelRequested: cancelRequestedAt != nil}, nil
}

func (store *Store) LoadRunExecution(ctx context.Context, runID string) (domain.Run, []domain.RunEvent, []domain.RunPayload, error) {
	run, err := scanRun(store.pool.QueryRow(ctx, `SELECT `+runSelectColumns+` FROM runs WHERE id=$1`, runID))
	if err != nil {
		return domain.Run{}, nil, nil, mapNotFound(err)
	}
	events := make([]domain.RunEvent, 0)
	var afterSequence int64
	for {
		page, err := store.ListRunEvents(ctx, runID, afterSequence, 200)
		if err != nil {
			return domain.Run{}, nil, nil, err
		}
		events = append(events, page...)
		if len(page) < 200 {
			break
		}
		afterSequence = page[len(page)-1].Sequence
	}
	rows, err := store.pool.Query(ctx, `SELECT run_id::text,sequence,kind,node_id,node_attempt,
		execution_protocol,cipher_version,ciphertext,created_at
		FROM run_payloads WHERE run_id=$1 ORDER BY sequence,kind,node_id,node_attempt`, runID)
	if err != nil {
		return domain.Run{}, nil, nil, fmt.Errorf("load run payloads: %w", err)
	}
	defer rows.Close()
	payloads := make([]domain.RunPayload, 0)
	for rows.Next() {
		var payload domain.RunPayload
		var nodeID *string
		var nodeAttempt *int
		if err := rows.Scan(&payload.RunID, &payload.Sequence, &payload.Kind, &nodeID, &nodeAttempt,
			&payload.ExecutionProtocol, &payload.CipherVersion, &payload.Ciphertext, &payload.CreatedAt); err != nil {
			return domain.Run{}, nil, nil, fmt.Errorf("scan run payload: %w", err)
		}
		if nodeID != nil {
			payload.NodeID = *nodeID
		}
		if nodeAttempt != nil {
			payload.NodeAttempt = *nodeAttempt
		}
		payloads = append(payloads, payload)
	}
	if err := rows.Err(); err != nil {
		return domain.Run{}, nil, nil, fmt.Errorf("read run payloads: %w", err)
	}
	return run, events, payloads, nil
}

func (store *Store) PersistLeasedRunEvent(ctx context.Context, lease domain.RunLease, event domain.RunEvent, nodeRun *domain.NodeRun, payloads []domain.RunPayload, budget domain.RunEventBudget) error {
	if err := validateLeasedEventWrite(lease, event, nodeRun, payloads); err != nil {
		return err
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin leased run event: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := assertRunLease(ctx, tx, lease); err != nil {
		return err
	}
	ensureNodeAttempt(&event, nodeRun)
	if err := persistRunEventData(ctx, tx, event, nodeRun, payloads, budget); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit leased run event: %w", err)
	}
	return nil
}

func (store *Store) RequireRunRecovery(ctx context.Context, lease domain.RunLease, event domain.RunEvent, reason domain.RunRecoveryReason, requestedAt time.Time, budget domain.RunEventBudget) error {
	if event.RunID != lease.RunID {
		return domain.ErrRunLeaseLost
	}
	if event.Type != "run.recovery_required" || requestedAt.IsZero() || reason == "" {
		return errors.New("invalid run recovery transition")
	}
	event.Timestamp = requestedAt
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin run recovery transition: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := assertRunLease(ctx, tx, lease); err != nil {
		return err
	}
	if err := persistRunEventData(ctx, tx, event, nil, nil, budget); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE runs SET status='recovery_required',recovery_reason=$2,recovery_requested_at=$3,
		lease_owner=NULL,lease_expires_at=NULL,heartbeat_at=NULL
		WHERE id=$1 AND lease_owner=$4 AND lease_token=$5`, lease.RunID, reason, requestedAt, lease.Owner, lease.Token)
	if err != nil {
		return fmt.Errorf("require run recovery: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunLeaseLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit run recovery transition: %w", err)
	}
	return nil
}

func (store *Store) FinalizeLeasedRun(ctx context.Context, lease domain.RunLease, finalization workflowservice.RunFinalization, payloads []domain.RunPayload) (domain.RunEvent, error) {
	if finalization.RunID != lease.RunID {
		return domain.RunEvent{}, domain.ErrRunLeaseLost
	}
	for _, payload := range payloads {
		if payload.RunID != lease.RunID {
			return domain.RunEvent{}, domain.ErrRunLeaseLost
		}
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.RunEvent{}, fmt.Errorf("begin leased run finalization: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := assertRunLease(ctx, tx, lease); err != nil {
		return domain.RunEvent{}, err
	}
	if err := insertRunPayloads(ctx, tx, payloads); err != nil {
		return domain.RunEvent{}, err
	}
	event, err := store.finalizeRunTx(ctx, tx, finalization, true)
	if err != nil {
		return domain.RunEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RunEvent{}, fmt.Errorf("commit leased run finalization: %w", err)
	}
	return event, nil
}

func assertRunLease(ctx context.Context, tx pgx.Tx, lease domain.RunLease) error {
	command, err := tx.Exec(ctx, `UPDATE runs SET heartbeat_at=clock_timestamp()
		WHERE id=$1 AND lease_owner=$2 AND lease_token=$3
			AND lease_expires_at>=clock_timestamp() AND status IN ('running','cancelling')`,
		lease.RunID, lease.Owner, lease.Token)
	if err != nil {
		return fmt.Errorf("verify run lease: %w", err)
	}
	if command.RowsAffected() != 1 {
		return domain.ErrRunLeaseLost
	}
	return nil
}

func persistRunEventData(ctx context.Context, tx pgx.Tx, event domain.RunEvent, nodeRun *domain.NodeRun, payloads []domain.RunPayload, budget domain.RunEventBudget) error {
	var count int
	var maxSequence, totalDataBytes int64
	if err := tx.QueryRow(ctx, `SELECT count(*),COALESCE(max(sequence),0),COALESCE(sum(data_bytes),0)
		FROM run_events WHERE run_id=$1`, event.RunID).Scan(&count, &maxSequence, &totalDataBytes); err != nil {
		return fmt.Errorf("read run event budget: %w", err)
	}
	if event.Sequence != maxSequence+1 {
		return fmt.Errorf("%w: sequence %d follows %d", domain.ErrRunEventSequence, event.Sequence, maxSequence)
	}
	if count >= budget.MaxEvents || event.DataBytes < 0 || event.DataBytes > budget.MaxTotalDataBytes-totalDataBytes {
		return fmt.Errorf("%w: events=%d bytes=%d", domain.ErrRunEventBudgetExceeded, count, totalDataBytes)
	}
	if err := insertRunEventRecord(ctx, tx, event); err != nil {
		return err
	}
	if nodeRun != nil {
		if err := upsertNodeRun(ctx, tx, *nodeRun); err != nil {
			return err
		}
	}
	return insertRunPayloads(ctx, tx, payloads)
}

func insertRunEventRecord(ctx context.Context, tx pgx.Tx, event domain.RunEvent) error {
	errorJSON, err := marshalOptional(event.Error)
	if err != nil {
		return fmt.Errorf("encode run event error: %w", err)
	}
	activePorts := nonNilStrings(event.ActivePorts)
	inputPaths := nonNilStrings(event.InputRedactedPaths)
	outputPaths := nonNilStrings(event.OutputRedactedPaths)
	if _, err := tx.Exec(ctx, `INSERT INTO run_events(
		run_id,sequence,type,node_id,node_attempt,status,input,output,active_ports,error,
		input_redacted_paths,output_redacted_paths,data_bytes,timestamp
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		event.RunID, event.Sequence, event.Type, nullableString(event.NodeID), event.NodeAttempt,
		nullableNodeStatus(event.Status), nullableRaw(event.Input), nullableRaw(event.Output), activePorts,
		errorJSON, inputPaths, outputPaths, event.DataBytes, event.Timestamp); err != nil {
		return fmt.Errorf("insert run event: %w", err)
	}
	return nil
}

func insertRunPayloads(ctx context.Context, tx pgx.Tx, payloads []domain.RunPayload) error {
	for _, payload := range payloads {
		var createdAt any
		if !payload.CreatedAt.IsZero() {
			createdAt = payload.CreatedAt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO run_payloads(
			run_id,sequence,kind,node_id,node_attempt,execution_protocol,cipher_version,ciphertext,created_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,COALESCE($9::timestamptz,clock_timestamp()))`,
			payload.RunID, payload.Sequence, payload.Kind, nullableString(payload.NodeID), nullablePositiveInt(payload.NodeAttempt),
			payload.ExecutionProtocol, payload.CipherVersion, payload.Ciphertext, createdAt); err != nil {
			return fmt.Errorf("insert run payload: %w", err)
		}
	}
	return nil
}

func nullablePositiveInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func validateLeasedEventWrite(lease domain.RunLease, event domain.RunEvent, nodeRun *domain.NodeRun, payloads []domain.RunPayload) error {
	if event.RunID != lease.RunID {
		return domain.ErrRunLeaseLost
	}
	if nodeRun != nil {
		if nodeRun.RunID != lease.RunID || nodeRun.NodeID != event.NodeID {
			return domain.ErrRunLeaseLost
		}
		if event.NodeAttempt != nil && nodeRun.Attempt > 0 && nodeRun.Attempt != *event.NodeAttempt {
			return domain.ErrRunLeaseLost
		}
	}
	for _, payload := range payloads {
		if payload.RunID != lease.RunID || payload.Sequence != event.Sequence {
			return domain.ErrRunLeaseLost
		}
		if payload.NodeID != event.NodeID || (event.NodeAttempt != nil && payload.NodeAttempt != *event.NodeAttempt) {
			return domain.ErrRunLeaseLost
		}
	}
	return nil
}
