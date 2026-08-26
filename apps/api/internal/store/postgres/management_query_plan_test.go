package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	workflowservice "github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func TestRunSummaryQueryPlansUseBoundedManagementIndexes(t *testing.T) {
	store := migratedTestStore(t)
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	const firstWorkflowID = "10000000-0000-4000-8000-000000000001"
	const secondWorkflowID = "10000000-0000-4000-8000-000000000002"
	const firstVersionID = "20000000-0000-4000-8000-000000000001"
	const secondVersionID = "20000000-0000-4000-8000-000000000002"
	if _, err := tx.Exec(ctx, `INSERT INTO workflows(id,name,slug,draft_graph,draft_revision)
		VALUES ($1,'一号','query-plan-one','{}',1),($2,'二号','query-plan-two','{}',1)`, firstWorkflowID, secondWorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_versions(id,workflow_id,version,graph,input_schema)
		VALUES ($1,$2,1,'{}','{}'),($3,$4,1,'{}','{}')`, firstVersionID, firstWorkflowID, secondVersionID, secondWorkflowID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO runs(
		id,workflow_id,workflow_version_id,draft_revision,graph_snapshot,mode,status,input,started_at,ended_at
	)
	SELECT md5(series::text)::uuid,
	       CASE WHEN series % 100 = 0 THEN $1::uuid ELSE $2::uuid END,
	       CASE WHEN series % 97 = 0 THEN
	         CASE WHEN series % 100 = 0 THEN $3::uuid ELSE $4::uuid END
	       ELSE NULL END,
	       CASE WHEN series % 97 = 0 THEN NULL ELSE 1 END,
	       CASE WHEN series % 97 = 0 THEN NULL ELSE '{}'::jsonb END,
	       CASE WHEN series % 97 = 0 THEN 'published' ELSE 'test' END,
	       CASE WHEN series % 89 = 0 THEN 'failed' ELSE 'running' END,
	       '{}'::jsonb,$5::timestamptz - series * interval '1 second',NULL
	FROM generate_series(1,100000) AS series`, firstWorkflowID, secondWorkflowID, firstVersionID, secondVersionID, time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "ANALYZE runs"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		query workflowservice.RunSummaryStoreQuery
		index string
	}{
		{name: "recent", query: workflowservice.RunSummaryStoreQuery{Limit: 50}, index: "runs_started_at_id_idx"},
		{name: "workflow", query: workflowservice.RunSummaryStoreQuery{WorkflowID: firstWorkflowID, Limit: 50}, index: "runs_workflow_started_at_id_idx"},
		{name: "status", query: workflowservice.RunSummaryStoreQuery{Statuses: []domain.RunStatus{domain.RunFailed}, Limit: 50}, index: "runs_status_started_at_id_idx"},
		{name: "cancelling status", query: workflowservice.RunSummaryStoreQuery{Statuses: []domain.RunStatus{domain.RunCancelling}, Limit: 50}, index: "runs_status_started_at_id_idx"},
		{name: "mode", query: workflowservice.RunSummaryStoreQuery{Modes: []domain.RunMode{domain.RunModePublished}, Limit: 50}, index: "runs_mode_started_at_id_idx"},
		{name: "workflow and status", query: workflowservice.RunSummaryStoreQuery{WorkflowID: firstWorkflowID, Statuses: []domain.RunStatus{domain.RunFailed}, Limit: 50}, index: "runs_workflow_status_started_at_id_idx"},
		{name: "workflow and mode", query: workflowservice.RunSummaryStoreQuery{WorkflowID: firstWorkflowID, Modes: []domain.RunMode{domain.RunModePublished}, Limit: 50}, index: "runs_workflow_mode_started_at_id_idx"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statement, arguments, err := buildRunSummaryQuery(test.query)
			if err != nil {
				t.Fatal(err)
			}
			var raw json.RawMessage
			if err := tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+statement, arguments...).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"Index Name": "`+test.index+`"`) {
				t.Fatalf("expected index %s, plan=%s", test.index, raw)
			}
		})
	}
}
