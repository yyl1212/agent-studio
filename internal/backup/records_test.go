package backup

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	recordUUID1 = "00000000-0000-0000-0000-000000000001"
	recordUUID2 = "00000000-0000-0000-0000-000000000002"
)

func TestValidateTableRecordAcceptsAllCompatibilityRecords(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	records := map[TableName]any{
		TableWorkflows: WorkflowRecord{
			ID: recordUUID1, Name: "Agent", Slug: "agent", Description: "", DraftGraph: json.RawMessage(`{}`),
			DraftRevision: 1, PublishedVersionID: nil, CreatedAt: now, UpdatedAt: now, ArchivedAt: nil,
			AgentPresentation: json.RawMessage(`{}`),
		},
		TableWorkflowVersions: WorkflowVersionRecord{
			ID: recordUUID2, WorkflowID: recordUUID1, Version: 1, Graph: json.RawMessage(`{}`),
			InputSchema: json.RawMessage(`{"type":"object"}`), CreatedAt: now, AgentPresentation: json.RawMessage(`{}`),
		},
		TableRuns: RunRecord{
			ID: recordUUID2, WorkflowID: recordUUID1, Mode: "test", Status: "completed", Input: json.RawMessage(`{}`),
			DraftRevision: pointer(int64(1)), GraphSnapshot: pointer(json.RawMessage(`{}`)), StartedAt: now, InputRedactedPaths: []string{},
		},
		TableNodeRuns: NodeRunRecord{
			ID: recordUUID2, RunID: recordUUID1, NodeID: "start", NodeType: "start", Status: "completed",
		},
		TableRunEvents: RunEventRecord{
			RunID: recordUUID1, Sequence: 1, Type: "run.started", ActivePorts: []string{},
			InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now,
		},
		TableWorkflowDraftCheckpoints: WorkflowDraftCheckpointRecord{
			WorkflowID: recordUUID1, SourceRevision: 1, RestoredRevision: 2, Graph: json.RawMessage(`{}`),
			AgentPresentation: json.RawMessage(`{}`), RestoredFromVersionID: recordUUID2, CreatedAt: now,
		},
	}
	for _, name := range TableOrder {
		name := name
		t.Run(string(name), func(t *testing.T) {
			raw, err := json.Marshal(records[name])
			if err != nil {
				t.Fatal(err)
			}
			if err := validateTableRecord(name, raw); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordDecodersRejectUnknownMissingAndTrailingFields(t *testing.T) {
	valid := WorkflowRecord{
		ID: recordUUID1, Name: "Agent", Slug: "agent", Description: "", DraftGraph: json.RawMessage(`{}`), DraftRevision: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), AgentPresentation: json.RawMessage(`{}`),
	}
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		append(append([]byte(nil), body[:len(body)-1]...), []byte(`,"unknown":true}`)...),
		[]byte(strings.Replace(string(body), `"description":"",`, "", 1)),
		append(append([]byte(nil), body...), []byte(` {}`)...),
	}
	for index, raw := range cases {
		if _, err := decodeWorkflowRecord(raw); CodeOf(err) != CodeArchiveInvalid {
			t.Fatalf("case %d code=%q err=%v", index, CodeOf(err), err)
		}
	}
}

func TestRecordDecodersRejectInvalidUUIDTimeJSONAndArrays(t *testing.T) {
	now := time.Now().UTC()
	validRun := RunRecord{
		ID: recordUUID1, WorkflowID: recordUUID2, Mode: "test", Status: "running", Input: json.RawMessage(`{}`),
		DraftRevision: pointer(int64(1)), GraphSnapshot: pointer(json.RawMessage(`{}`)), StartedAt: now, InputRedactedPaths: []string{},
	}
	for _, test := range []struct {
		name   string
		mutate func(*RunRecord)
	}{
		{name: "uuid", mutate: func(record *RunRecord) { record.ID = "not-a-uuid" }},
		{name: "time", mutate: func(record *RunRecord) { record.StartedAt = time.Time{} }},
		{name: "required json object", mutate: func(record *RunRecord) { record.Input = json.RawMessage(`[]`) }},
		{name: "nil array", mutate: func(record *RunRecord) { record.InputRedactedPaths = nil }},
		{name: "mode", mutate: func(record *RunRecord) { record.Mode = "unknown" }},
		{name: "status", mutate: func(record *RunRecord) { record.Status = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := validRun
			test.mutate(&record)
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeRunRecord(raw); CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestRunRecordDecoderAcceptsLocalRelationshipSemanticCases(t *testing.T) {
	valid := RunRecord{
		ID: recordUUID1, WorkflowID: recordUUID2, Mode: "test", Status: "running", Input: json.RawMessage(`{}`),
		DraftRevision: pointer(int64(1)), GraphSnapshot: pointer(json.RawMessage(`{}`)), StartedAt: time.Now().UTC(),
		InputRedactedPaths: []string{},
	}
	for _, test := range []struct {
		name   string
		mutate func(*RunRecord)
	}{
		{name: "test missing draft revision", mutate: func(record *RunRecord) { record.DraftRevision = nil }},
		{name: "test has version", mutate: func(record *RunRecord) { record.WorkflowVersionID = pointer(recordUUID2) }},
		{name: "published missing version", mutate: func(record *RunRecord) {
			record.Mode = "published"
			record.DraftRevision = nil
			record.GraphSnapshot = nil
		}},
		{name: "published has snapshot", mutate: func(record *RunRecord) { record.Mode = "published"; record.WorkflowVersionID = pointer(recordUUID2) }},
		{name: "debug missing source", mutate: func(record *RunRecord) { record.Mode = "debug"; record.DraftRevision = nil }},
		{name: "retry pair", mutate: func(record *RunRecord) { record.RetryOfRunID = pointer(recordUUID2) }},
		{name: "agent key on test", mutate: func(record *RunRecord) { record.AgentRequestKey = pointer(recordUUID2) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeRunRecord(raw); err != nil {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRunEventDecoderRejectsInvalidIntegerEnumAndNilArrays(t *testing.T) {
	valid := RunEventRecord{
		RunID: recordUUID1, Sequence: 1, Type: "node.started", Status: pointer("running"), ActivePorts: []string{},
		InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: time.Now().UTC(),
	}
	for _, mutate := range []func(*RunEventRecord){
		func(record *RunEventRecord) { record.Sequence = 0 },
		func(record *RunEventRecord) { record.DataBytes = -1 },
		func(record *RunEventRecord) { record.Type = "unknown" },
		func(record *RunEventRecord) { record.Status = pointer("unknown") },
		func(record *RunEventRecord) { record.ActivePorts = nil },
		func(record *RunEventRecord) { record.InputRedactedPaths = nil },
		func(record *RunEventRecord) { record.OutputRedactedPaths = nil },
	} {
		record := valid
		mutate(&record)
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRunEventRecord(raw); CodeOf(err) != CodeArchiveInvalid {
			t.Fatalf("record=%s code=%q err=%v", raw, CodeOf(err), err)
		}
	}
}

func pointer[T any](value T) *T { return &value }
