package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentstudio.local/api/internal/domain"
)

const runSelectColumns = `id::text,workflow_id::text,workflow_version_id::text,draft_revision,
    graph_snapshot,mode,status,input,output,error,started_at,ended_at`

func (store *Store) CreateRun(ctx context.Context, run domain.Run) error {
	errorJSON, err := marshalOptional(run.Error)
	if err != nil {
		return fmt.Errorf("encode run error: %w", err)
	}
	_, err = store.pool.Exec(ctx, `INSERT INTO runs(
        id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,mode,status,input,output,error,started_at,ended_at
    ) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		run.ID, run.WorkflowID, run.WorkflowVersionID, run.DraftRevision, nullableRaw(run.GraphSnapshot),
		run.Mode, run.Status, run.Input, nullableRaw(run.Output), errorJSON, run.StartedAt, run.EndedAt,
	)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	return nil
}

func (store *Store) UpsertNodeRun(ctx context.Context, nodeRun domain.NodeRun) error {
	errorJSON, err := marshalOptional(nodeRun.Error)
	if err != nil {
		return fmt.Errorf("encode node run error: %w", err)
	}
	_, err = store.pool.Exec(ctx, `INSERT INTO node_runs(
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
		&graphSnapshot, &run.Mode, &run.Status, &input, &output, &errorJSON,
		&run.StartedAt, &run.EndedAt,
	); err != nil {
		return domain.Run{}, err
	}
	run.GraphSnapshot = json.RawMessage(graphSnapshot)
	run.Input = json.RawMessage(input)
	run.Output = json.RawMessage(output)
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
