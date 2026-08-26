package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (store *Store) CreateAgentRun(ctx context.Context, run domain.Run) (domain.Run, bool, error) {
	if run.Mode != domain.RunModePublished || run.WorkflowVersionID == nil || run.AgentRequestKey == nil || *run.AgentRequestKey == "" {
		return domain.Run{}, false, workflowservice.ErrInvalidWorkflowInput
	}
	if err := store.CreateRun(ctx, run); err == nil {
		return run, true, nil
	} else {
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "23505" || databaseError.ConstraintName != "runs_agent_request_key_unique_idx" {
			return domain.Run{}, false, err
		}
	}
	existing, err := scanRun(store.pool.QueryRow(ctx, `SELECT `+runSelectColumns+`
		FROM runs WHERE workflow_id=$1 AND agent_request_key=$2 AND mode='published'`, run.WorkflowID, *run.AgentRequestKey))
	if err != nil {
		return domain.Run{}, false, fmt.Errorf("load existing agent run: %w", mapNotFound(err))
	}
	return existing, false, nil
}

func (store *Store) FindAgentRunByRequestKey(ctx context.Context, slug, requestKey string) (workflowservice.AgentRunRecord, error) {
	run, err := scanRun(store.pool.QueryRow(ctx, `SELECT `+runSelectColumns+`
		FROM runs r WHERE r.agent_request_key=$2 AND r.mode='published'
		AND EXISTS (SELECT 1 FROM workflows w WHERE w.id=r.workflow_id AND w.slug=$1 AND w.archived_at IS NULL)`, slug, requestKey))
	if err != nil {
		return workflowservice.AgentRunRecord{}, mapNotFound(err)
	}
	version, err := loadAgentRunVersion(ctx, store.pool, run)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	return workflowservice.AgentRunRecord{Run: run, Version: version, Events: []domain.RunEvent{}}, nil
}

func (store *Store) GetAgentRun(ctx context.Context, slug, runID string, afterSequence int64, limit int) (workflowservice.AgentRunRecord, error) {
	limit = normalizeAgentEventLimit(limit)
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return workflowservice.AgentRunRecord{}, fmt.Errorf("begin agent run snapshot: %w", err)
	}
	defer transaction.Rollback(ctx)
	record, err := loadAgentRunRecord(ctx, transaction, slug, runID, afterSequence, limit)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return workflowservice.AgentRunRecord{}, fmt.Errorf("commit agent run snapshot: %w", err)
	}
	return record, nil
}

func (store *Store) RequestAgentRunCancel(ctx context.Context, slug, runID string) (workflowservice.AgentRunRecord, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return workflowservice.AgentRunRecord{}, fmt.Errorf("begin agent run cancellation: %w", err)
	}
	defer transaction.Rollback(ctx)
	run, err := scanRun(transaction.QueryRow(ctx, `SELECT `+runSelectColumns+`
		FROM runs r WHERE r.id=$2 AND r.mode='published'
		AND EXISTS (SELECT 1 FROM workflows w WHERE w.id=r.workflow_id AND w.slug=$1 AND w.archived_at IS NULL)
		FOR UPDATE`, slug, runID))
	if err != nil {
		return workflowservice.AgentRunRecord{}, mapNotFound(err)
	}
	switch run.Status {
	case domain.RunRunning:
		run.Status = domain.RunCancelling
		if err := transaction.QueryRow(ctx, `UPDATE runs SET status='cancelling',cancel_requested_at=clock_timestamp()
			WHERE id=$1 RETURNING cancel_requested_at`, run.ID).Scan(&run.CancelRequestedAt); err != nil {
			return workflowservice.AgentRunRecord{}, fmt.Errorf("request agent run cancellation: %w", err)
		}
	case domain.RunCancelling, domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
	default:
		return workflowservice.AgentRunRecord{}, fmt.Errorf("invalid agent run status %q", run.Status)
	}
	version, err := loadAgentRunVersion(ctx, transaction, run)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	events, hasMore, err := loadAgentRunEvents(ctx, transaction, run.ID, 0, 200)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return workflowservice.AgentRunRecord{}, fmt.Errorf("commit agent run cancellation: %w", err)
	}
	return workflowservice.AgentRunRecord{Run: run, Version: version, Events: events, HasMore: hasMore}, nil
}

type agentRunQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadAgentRunRecord(ctx context.Context, queryer agentRunQueryer, slug, runID string, afterSequence int64, limit int) (workflowservice.AgentRunRecord, error) {
	run, err := scanRun(queryer.QueryRow(ctx, `SELECT `+runSelectColumns+`
		FROM runs r WHERE r.id=$2 AND r.mode='published'
		AND EXISTS (SELECT 1 FROM workflows w WHERE w.id=r.workflow_id AND w.slug=$1 AND w.archived_at IS NULL)`, slug, runID))
	if err != nil {
		return workflowservice.AgentRunRecord{}, mapNotFound(err)
	}
	version, err := loadAgentRunVersion(ctx, queryer, run)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	events, hasMore, err := loadAgentRunEvents(ctx, queryer, run.ID, afterSequence, limit)
	if err != nil {
		return workflowservice.AgentRunRecord{}, err
	}
	return workflowservice.AgentRunRecord{Run: run, Version: version, Events: events, HasMore: hasMore}, nil
}

func loadAgentRunVersion(ctx context.Context, queryer agentRunQueryer, run domain.Run) (domain.WorkflowVersion, error) {
	if run.WorkflowVersionID == nil {
		return domain.WorkflowVersion{}, domain.ErrNotFound
	}
	var version domain.WorkflowVersion
	var graph, inputSchema, presentation []byte
	if err := queryer.QueryRow(ctx, `SELECT id::text,workflow_id::text,version,graph,input_schema,agent_presentation,created_at
		FROM workflow_versions WHERE id=$1 AND workflow_id=$2`, *run.WorkflowVersionID, run.WorkflowID).Scan(
		&version.ID, &version.WorkflowID, &version.Version, &graph, &inputSchema, &presentation, &version.CreatedAt,
	); err != nil {
		return domain.WorkflowVersion{}, mapNotFound(err)
	}
	version.Graph = json.RawMessage(graph)
	version.InputSchema = json.RawMessage(inputSchema)
	if err := json.Unmarshal(presentation, &version.AgentPresentation); err != nil {
		return domain.WorkflowVersion{}, fmt.Errorf("decode agent run presentation: %w", err)
	}
	return version, nil
}

func loadAgentRunEvents(ctx context.Context, queryer agentRunQueryer, runID string, afterSequence int64, limit int) ([]domain.RunEvent, bool, error) {
	rows, err := queryer.Query(ctx, `SELECT run_id::text,sequence,type,node_id,status,input,output,
		active_ports,error,input_redacted_paths,output_redacted_paths,data_bytes,timestamp
		FROM run_events WHERE run_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, runID, afterSequence, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list agent run events: %w", err)
	}
	defer rows.Close()
	events := make([]domain.RunEvent, 0, limit)
	for rows.Next() {
		event, err := scanRunEvent(rows)
		if err != nil {
			return nil, false, fmt.Errorf("scan agent run event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read agent run events: %w", err)
	}
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return events, hasMore, nil
}

func normalizeAgentEventLimit(limit int) int {
	if limit <= 0 {
		return 200
	}
	if limit > 200 {
		return 200
	}
	return limit
}
