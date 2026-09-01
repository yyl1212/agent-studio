package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

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
	case domain.RunQueued, domain.RunRecoveryRequired:
		now := time.Now().UTC()
		var nextSequence int64
		if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM run_events WHERE run_id=$1`, runID).Scan(&nextSequence); err != nil {
			return domain.RunSummary{}, fmt.Errorf("read cancellation event sequence: %w", err)
		}
		if _, err := transaction.Exec(ctx, `UPDATE runs SET cancel_requested_at=$2 WHERE id=$1`, runID, now); err != nil {
			return domain.RunSummary{}, fmt.Errorf("request inactive run cancellation: %w", err)
		}
		if _, err := store.finalizeRunTx(ctx, transaction, workflowservice.RunFinalization{
			RunID: runID, Status: domain.RunCancelled, EndedAt: now,
			TerminalEvent: domain.RunEvent{RunID: runID, Sequence: nextSequence, Type: "run.cancelled", Timestamp: now},
			Budget:        domain.RunEventBudget{MaxEvents: 16, MaxTotalDataBytes: 32 << 20},
		}, true); err != nil {
			return domain.RunSummary{}, err
		}
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
