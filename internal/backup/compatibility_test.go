package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCurrentRuntimeRestoresV1Alpha1GoldenArchive(t *testing.T) {
	target := openUnmigratedTarget(t)
	path := filepath.Join("testdata", "v1alpha1-minimal.asbak")
	if _, err := Inspect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := DryRun(context.Background(), target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), target, path); err != nil {
		t.Fatal(err)
	}
	assertGoldenDomainData(t, target)
}

func assertGoldenDomainData(t *testing.T, target *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	const (
		workflowID = "00000000-0000-0000-0000-000000001001"
		versionID  = "00000000-0000-0000-0000-000000002001"
		published  = "00000000-0000-0000-0000-000000003001"
		debug      = "00000000-0000-0000-0000-000000003002"
		retry      = "00000000-0000-0000-0000-000000003003"
	)
	var name, slug, publishedVersion string
	var revision int64
	if err := target.QueryRow(ctx, `SELECT name,slug,published_version_id::text,draft_revision FROM workflows WHERE id=$1`, workflowID).
		Scan(&name, &slug, &publishedVersion, &revision); err != nil {
		t.Fatal(err)
	}
	if name != "Golden v1alpha1 Workflow" || slug != "golden-v1alpha1" || publishedVersion != versionID || revision != 7 {
		t.Fatalf("workflow name=%q slug=%q published=%q revision=%d", name, slug, publishedVersion, revision)
	}
	var version int
	var nodeID, nodeType string
	if err := target.QueryRow(ctx, `SELECT version,graph->'nodes'->0->>'id',graph->'nodes'->0->>'type' FROM workflow_versions WHERE id=$1 AND workflow_id=$2`, versionID, workflowID).
		Scan(&version, &nodeID, &nodeType); err != nil {
		t.Fatal(err)
	}
	if version != 1 || nodeID != "answer" || nodeType != "fixture.answer" {
		t.Fatalf("version=%d nodeID=%q nodeType=%q", version, nodeID, nodeType)
	}
	var mode, status string
	var workflowVersion, sourceRun, retryOf *string
	if err := target.QueryRow(ctx, `SELECT mode,status,workflow_version_id::text,source_run_id::text,retry_of_run_id::text FROM runs WHERE id=$1`, published).
		Scan(&mode, &status, &workflowVersion, &sourceRun, &retryOf); err != nil {
		t.Fatal(err)
	}
	if mode != "published" || status != "completed" || workflowVersion == nil || *workflowVersion != versionID || sourceRun != nil || retryOf != nil {
		t.Fatalf("published mode=%s status=%s version=%v source=%v retry=%v", mode, status, workflowVersion, sourceRun, retryOf)
	}
	if err := target.QueryRow(ctx, `SELECT mode,status,source_run_id::text,retry_of_run_id::text FROM runs WHERE id=$1`, debug).
		Scan(&mode, &status, &sourceRun, &retryOf); err != nil {
		t.Fatal(err)
	}
	if mode != "debug" || status != "completed" || sourceRun == nil || *sourceRun != published || retryOf != nil {
		t.Fatalf("debug mode=%s status=%s source=%v retry=%v", mode, status, sourceRun, retryOf)
	}
	if err := target.QueryRow(ctx, `SELECT mode,status,source_run_id::text,retry_of_run_id::text FROM runs WHERE id=$1`, retry).
		Scan(&mode, &status, &sourceRun, &retryOf); err != nil {
		t.Fatal(err)
	}
	if mode != "debug" || status != "completed" || sourceRun == nil || *sourceRun != published || retryOf == nil || *retryOf != debug {
		t.Fatalf("retry mode=%s status=%s source=%v retry=%v", mode, status, sourceRun, retryOf)
	}
	var nodeDistribution, eventDistribution string
	if err := target.QueryRow(ctx, `SELECT string_agg(run_id::text || ':' || node_id || ':' || status, ',' ORDER BY run_id,id)
		FROM node_runs WHERE run_id IN ($1,$2,$3)`, published, debug, retry).Scan(&nodeDistribution); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(ctx, `SELECT string_agg(run_id::text || ':' || sequence::text || ':' || type, ',' ORDER BY run_id,sequence)
		FROM run_events WHERE run_id IN ($1,$2,$3)`, published, debug, retry).Scan(&eventDistribution); err != nil {
		t.Fatal(err)
	}
	wantNodes := published + ":answer:completed," + debug + ":answer:completed," + retry + ":answer:completed"
	wantEvents := published + ":1:run.started," + published + ":2:run.completed," +
		debug + ":1:run.started," + debug + ":2:run.completed," +
		retry + ":1:run.started," + retry + ":2:run.completed"
	if nodeDistribution != wantNodes || eventDistribution != wantEvents {
		t.Fatalf("node distribution=%q want=%q event distribution=%q want=%q", nodeDistribution, wantNodes, eventDistribution, wantEvents)
	}
	var publishedGraphNull bool
	var publishedOutput, debugGraph, debugOutput, retryError, nodeInput, nodeOutput *string
	if err := target.QueryRow(ctx, `SELECT graph_snapshot IS NULL,output::text FROM runs WHERE id=$1`, published).
		Scan(&publishedGraphNull, &publishedOutput); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(ctx, `SELECT graph_snapshot::text,output::text FROM runs WHERE id=$1`, debug).
		Scan(&debugGraph, &debugOutput); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(ctx, `SELECT error::text FROM runs WHERE id=$1`, retry).Scan(&retryError); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(ctx, `SELECT input::text,output::text FROM node_runs WHERE run_id=$1`, debug).
		Scan(&nodeInput, &nodeOutput); err != nil {
		t.Fatal(err)
	}
	if !publishedGraphNull || publishedOutput == nil || *publishedOutput != "null" ||
		debugGraph == nil || *debugGraph != `{"nodes": [{"id": "answer", "type": "fixture.answer"}]}` ||
		debugOutput == nil || *debugOutput != "[]" || retryError == nil || *retryError != "null" ||
		nodeInput == nil || *nodeInput != "null" || nodeOutput == nil || *nodeOutput != "[]" {
		t.Fatalf("golden nullable jsonb graphNull=%t publishedOutput=%v debugGraph=%v debugOutput=%v retryError=%v nodeInput=%v nodeOutput=%v",
			publishedGraphNull, publishedOutput, debugGraph, debugOutput, retryError, nodeInput, nodeOutput)
	}
	var sourceRevision, restoredRevision int64
	var restoredFrom string
	if err := target.QueryRow(ctx, `SELECT source_revision,restored_revision,restored_from_version_id::text FROM workflow_draft_checkpoints WHERE workflow_id=$1`, workflowID).
		Scan(&sourceRevision, &restoredRevision, &restoredFrom); err != nil {
		t.Fatal(err)
	}
	if sourceRevision != 7 || restoredRevision != 8 || restoredFrom != versionID {
		t.Fatalf("checkpoint source=%d restored=%d version=%s", sourceRevision, restoredRevision, restoredFrom)
	}
}
