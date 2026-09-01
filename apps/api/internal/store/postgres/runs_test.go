package postgres

import (
	"context"
	"strings"
	"testing"
)

func TestDurableSchemaRejectsInvalidRunData(t *testing.T) {
	store := migratedTestStore(t)
	workflow := createWorkflowFixture(t, store, "durable-schema")
	run := newTestRun(workflow.ID, workflow.DraftRevision, workflow.DraftGraph)
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}

	if _, err := store.pool.Exec(context.Background(), "UPDATE runs SET status='invalid' WHERE id=$1", run.ID); err == nil {
		t.Fatal("invalid run status was accepted")
	}
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO node_runs(id,run_id,node_id,node_type,status,attempt)
		VALUES($1,$2,'node-negative','echo','running',-1)`, fixtureUUID(), run.ID); err == nil {
		t.Fatal("negative node attempt was accepted")
	}

	const insertPayload = `
		INSERT INTO run_payloads(run_id,sequence,kind,execution_protocol,cipher_version,ciphertext)
		VALUES($1,0,'run_input',1,1,$2)`
	if _, err := store.pool.Exec(context.Background(), insertPayload, run.ID, []byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(context.Background(), insertPayload, run.ID, []byte{2}); err == nil {
		t.Fatal("duplicate run payload identity was accepted")
	}
}

func TestDurableSchemaHotQueriesUseIndexes(t *testing.T) {
	store := migratedTestStore(t)
	tests := []struct {
		name      string
		query     string
		indexName string
	}{
		{
			name:      "claimable runs",
			query:     `SELECT id FROM runs WHERE status IN ('queued','running','cancelling') ORDER BY started_at,id LIMIT 1`,
			indexName: "runs_claimable_idx",
		},
		{
			name:      "recovery management",
			query:     `SELECT id FROM runs WHERE status='recovery_required' ORDER BY started_at DESC,id DESC LIMIT 50`,
			indexName: "runs_recovery_required_idx",
		},
		{
			name:      "active leases by owner",
			query:     `SELECT id FROM runs WHERE lease_owner='worker-1' AND status IN ('running','cancelling') ORDER BY lease_expires_at,id`,
			indexName: "runs_lease_owner_idx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx, err := store.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err := tx.Exec(context.Background(), "SET LOCAL enable_seqscan=off"); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.Query(context.Background(), "EXPLAIN "+tt.query)
			if err != nil {
				t.Fatal(err)
			}
			var plan strings.Builder
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				plan.WriteString(line)
				plan.WriteByte('\n')
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), tt.indexName) {
				t.Fatalf("query plan did not use %s:\n%s", tt.indexName, plan.String())
			}
		})
	}
}
