package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) HeartbeatRuns(ctx context.Context, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	rows, err := store.pool.Query(ctx, `WITH updated AS (
		UPDATE runs SET heartbeat_at=clock_timestamp()
		WHERE id=ANY($1::uuid[]) AND status IN ('running','cancelling')
		RETURNING id,cancel_requested_at
	) SELECT id::text FROM updated WHERE cancel_requested_at IS NOT NULL ORDER BY id`, ids)
	if err != nil {
		return nil, fmt.Errorf("heartbeat runs: %w", err)
	}
	defer rows.Close()
	cancelled := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan heartbeat cancellation: %w", err)
		}
		cancelled = append(cancelled, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read heartbeat cancellations: %w", err)
	}
	return cancelled, nil
}

func (store *Store) RequestRunCancel(ctx context.Context, runID string) (domain.RunSummary, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return domain.RunSummary{}, fmt.Errorf("begin request run cancel: %w", err)
	}
	defer transaction.Rollback(ctx)
	var status domain.RunStatus
	if err := transaction.QueryRow(ctx, "SELECT status FROM runs WHERE id=$1 FOR UPDATE", runID).Scan(&status); err != nil {
		return domain.RunSummary{}, fmt.Errorf("lock run for cancellation: %w", mapNotFound(err))
	}
	switch status {
	case domain.RunRunning:
		if _, err := transaction.Exec(ctx, `UPDATE runs
			SET status='cancelling',cancel_requested_at=clock_timestamp()
			WHERE id=$1`, runID); err != nil {
			return domain.RunSummary{}, fmt.Errorf("request run cancellation: %w", err)
		}
	case domain.RunCancelling:
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
		return domain.RunSummary{}, workflowservice.ErrRunNotCancellable
	default:
		return domain.RunSummary{}, fmt.Errorf("invalid run status %q", status)
	}
	summary, err := scanRunSummary(transaction.QueryRow(ctx, `SELECT `+runSummarySelectColumns+`
		FROM runs r
		JOIN workflows w ON w.id=r.workflow_id
		LEFT JOIN workflow_versions rv ON rv.id=r.workflow_version_id AND rv.workflow_id=r.workflow_id
		WHERE r.id=$1`, runID))
	if err != nil {
		return domain.RunSummary{}, fmt.Errorf("read cancelled run summary: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return domain.RunSummary{}, fmt.Errorf("commit run cancellation: %w", err)
	}
	return summary, nil
}

type interruptedRunCandidate struct {
	id           string
	nodes        json.RawMessage
	nextSequence int64
}

func (store *Store) FinalizeInterruptedRuns(ctx context.Context, staleAfterSeconds, limit int) (int, error) {
	if staleAfterSeconds <= 0 || limit <= 0 || limit > 500 {
		return 0, fmt.Errorf("invalid interrupted run sweep bounds")
	}
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin interrupted run sweep: %w", err)
	}
	defer transaction.Rollback(ctx)
	var databaseNow time.Time
	if err := transaction.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&databaseNow); err != nil {
		return 0, fmt.Errorf("read database clock: %w", err)
	}
	rows, err := transaction.Query(ctx, `SELECT r.id::text,
		CASE WHEN r.mode IN ('test','debug') THEN r.graph_snapshot->'nodes' ELSE version.graph->'nodes' END,
		COALESCE((SELECT max(event.sequence) FROM run_events event WHERE event.run_id=r.id),0)+1
	FROM runs r
	LEFT JOIN workflow_versions version
		ON version.id=r.workflow_version_id AND version.workflow_id=r.workflow_id
	WHERE r.status IN ('running','cancelling')
		AND COALESCE(r.heartbeat_at,r.started_at) < $1::timestamptz - make_interval(secs => $2)
	ORDER BY r.started_at,r.id
	FOR UPDATE OF r SKIP LOCKED
	LIMIT $3`, databaseNow, staleAfterSeconds, limit)
	if err != nil {
		return 0, fmt.Errorf("select interrupted runs: %w", err)
	}
	candidates := make([]interruptedRunCandidate, 0, limit)
	for rows.Next() {
		var candidate interruptedRunCandidate
		if err := rows.Scan(&candidate.id, &candidate.nodes, &candidate.nextSequence); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan interrupted run: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("read interrupted runs: %w", err)
	}
	rows.Close()
	interruptedError := &domain.PublicError{Code: "RUN_INTERRUPTED", Message: "运行进程已中断"}
	for _, candidate := range candidates {
		nodeCount, err := interruptedNodeCount(candidate.nodes)
		if err != nil {
			return 0, fmt.Errorf("read interrupted run %s graph: %w", candidate.id, err)
		}
		_, err = store.finalizeRunTx(ctx, transaction, workflowservice.RunFinalization{
			RunID: candidate.id, Status: domain.RunCancelled, Error: interruptedError, EndedAt: databaseNow,
			TerminalEvent: domain.RunEvent{
				RunID: candidate.id, Sequence: candidate.nextSequence, Type: "run.cancelled", Error: interruptedError,
				ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: databaseNow,
			},
			Budget: domain.RunEventBudget{MaxEvents: 2*nodeCount + 2, MaxTotalDataBytes: 16 << 20},
		})
		if err != nil {
			return 0, fmt.Errorf("finalize interrupted run %s: %w", candidate.id, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit interrupted run sweep: %w", err)
	}
	return len(candidates), nil
}

func interruptedNodeCount(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var nodes []json.RawMessage
	if err := decoder.Decode(&nodes); err != nil || nodes == nil {
		return 0, fmt.Errorf("nodes must be an array")
	}
	if decoder.More() {
		return 0, fmt.Errorf("nodes contain trailing data")
	}
	return len(nodes), nil
}
