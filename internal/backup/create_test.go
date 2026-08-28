package backup

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectRejectsInvalidTypedRecords(t *testing.T) {
	for _, test := range []struct {
		name   string
		table  TableName
		mutate func(map[string]any)
	}{
		{name: "unknown field", table: TableWorkflows, mutate: func(record map[string]any) { record["unknown"] = true }},
		{name: "invalid uuid", table: TableWorkflows, mutate: func(record map[string]any) { record["id"] = "not-a-uuid" }},
		{name: "invalid time", table: TableWorkflows, mutate: func(record map[string]any) { record["createdAt"] = "0001-01-01T00:00:00Z" }},
		{name: "non object jsonb", table: TableWorkflows, mutate: func(record map[string]any) { record["draftGraph"] = []any{} }},
		{name: "nil array", table: TableRuns, mutate: func(record map[string]any) { record["inputRedactedPaths"] = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bodies := validTypedRecordBodies(t)
			var record map[string]any
			if err := json.Unmarshal(bodies[test.table], &record); err != nil {
				t.Fatal(err)
			}
			test.mutate(record)
			body, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			bodies[test.table] = body
			output := filepath.Join(t.TempDir(), "invalid.asbak")
			if _, err := WriteArchive(context.Background(), output, manifestFixture(time.Now().UTC()), typedRecordWriters(bodies)); err != nil {
				t.Fatal(err)
			}
			if _, err := Inspect(context.Background(), output); CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func validTypedRecordBodies(t *testing.T) map[TableName][]byte {
	t.Helper()
	now := time.Now().UTC()
	records := map[TableName]any{
		TableWorkflows: WorkflowRecord{
			ID: recordUUID1, Name: "Agent", Slug: "agent", DraftGraph: json.RawMessage(`{}`), DraftRevision: 1,
			CreatedAt: now, UpdatedAt: now, AgentPresentation: json.RawMessage(`{}`),
		},
		TableWorkflowVersions: WorkflowVersionRecord{
			ID: recordUUID2, WorkflowID: recordUUID1, Version: 1, Graph: json.RawMessage(`{}`),
			InputSchema: json.RawMessage(`{}`), CreatedAt: now, AgentPresentation: json.RawMessage(`{}`),
		},
		TableRuns: RunRecord{
			ID: recordUUID2, WorkflowID: recordUUID1, Mode: "test", Status: "running", Input: json.RawMessage(`{}`),
			StartedAt: now, InputRedactedPaths: []string{},
		},
		TableNodeRuns: NodeRunRecord{
			ID: recordUUID2, RunID: recordUUID1, NodeID: "start", NodeType: "start", Status: "running",
		},
		TableRunEvents: RunEventRecord{
			RunID: recordUUID1, Sequence: 1, Type: "run.started", ActivePorts: []string{}, InputRedactedPaths: []string{},
			OutputRedactedPaths: []string{}, Timestamp: now,
		},
		TableWorkflowDraftCheckpoints: WorkflowDraftCheckpointRecord{
			WorkflowID: recordUUID1, SourceRevision: 1, RestoredRevision: 2, Graph: json.RawMessage(`{}`),
			AgentPresentation: json.RawMessage(`{}`), RestoredFromVersionID: recordUUID2, CreatedAt: now,
		},
	}
	bodies := make(map[TableName][]byte, len(records))
	for name, record := range records {
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		bodies[name] = body
	}
	return bodies
}

func typedRecordWriters(bodies map[TableName][]byte) map[TableName]TableWriter {
	writers := make(map[TableName]TableWriter, len(TableOrder))
	for _, name := range TableOrder {
		name := name
		writers[name] = func(_ context.Context, writer io.Writer) (TableManifest, error) {
			body := append(append([]byte(nil), bodies[name]...), '\n')
			if _, err := writer.Write(body); err != nil {
				return TableManifest{}, err
			}
			path, _ := tablePath(name)
			return TableManifest{Name: name, Path: path, Records: 1, UncompressedBytes: uint64(len(body)), Digest: digest(body)}, nil
		}
	}
	return writers
}
