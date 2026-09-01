package backup

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestV1Alpha2PayloadUsesStrictBase64AndMetadata(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	record := RunPayloadRecord{
		RunID: recordUUID1, Sequence: 0, Kind: "run_input", ExecutionProtocol: 1,
		CipherVersion: 1, Ciphertext: base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 0xff}), CreatedAt: now,
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeRunPayloadRecord(body)
	if err != nil || decoded.Ciphertext != record.Ciphertext {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}

	for _, invalid := range []string{"AA", "AA==\n", "", "!!!!"} {
		record.Ciphertext = invalid
		body, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRunPayloadRecord(body); CodeOf(err) != CodeArchiveInvalid {
			t.Fatalf("ciphertext=%q code=%q err=%v", invalid, CodeOf(err), err)
		}
	}
}

func TestV1Alpha2RunNodeAndEventRequireDurableFields(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	run := RunRecordV1Alpha2{RunRecord: RunRecord{
		ID: recordUUID2, WorkflowID: recordUUID1, DraftRevision: pointer(int64(1)), GraphSnapshot: presentJSONB(`{}`),
		Mode: "test", Status: "queued", Input: json.RawMessage(`{}`), InputRedactedPaths: []string{}, StartedAt: now,
	}, ExecutionProtocol: 1}
	node := NodeRunRecordV1Alpha2{NodeRunRecord: NodeRunRecord{ID: recordUUID2, RunID: recordUUID1, NodeID: "work", NodeType: "test", Status: "running"}, Attempt: 1}
	event := RunEventRecordV1Alpha2{RunEventRecord: RunEventRecord{RunID: recordUUID1, Sequence: 1, Type: "run.queued", ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now}}
	for table, record := range map[TableName]any{TableRuns: run, TableNodeRuns: node, TableRunEvents: event} {
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateTableRecordForVersion(APIVersionV1Alpha2, table, body); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
	}
}

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
			DraftRevision: pointer(int64(1)), GraphSnapshot: presentJSONB(`{}`), StartedAt: now, InputRedactedPaths: []string{},
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
	for _, name := range TableOrderV1Alpha1 {
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

func TestRecordDecoderRejectsAmbiguousAndInvalidNullableJSONBWire(t *testing.T) {
	const oldAmbiguous = `{"id":"00000000-0000-0000-0000-000000000001","workflowId":"00000000-0000-0000-0000-000000000002","workflowVersionId":"00000000-0000-0000-0000-000000000002","draftRevision":null,"graphSnapshot":null,"mode":"published","status":"completed","input":{},"output":null,"error":null,"startedAt":"2026-08-29T10:00:00Z","endedAt":null,"sourceRunId":null,"sourceNodeId":null,"retryOfRunId":null,"retryKey":null,"inputRedactedPaths":[],"cancelRequestedAt":null,"heartbeatAt":null,"agentRequestKey":null}`
	const invalidState = `{"id":"00000000-0000-0000-0000-000000000001","workflowId":"00000000-0000-0000-0000-000000000002","workflowVersionId":null,"draftRevision":1,"graphSnapshot":{"valid":true,"value":{}},"mode":"test","status":"completed","input":{},"output":{"valid":false,"value":{}},"error":{"valid":false,"value":null},"startedAt":"2026-08-29T10:00:00Z","endedAt":null,"sourceRunId":null,"sourceNodeId":null,"retryOfRunId":null,"retryKey":null,"inputRedactedPaths":[],"cancelRequestedAt":null,"heartbeatAt":null,"agentRequestKey":null}`
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "legacy ambiguous null", raw: oldAmbiguous},
		{name: "invalid false state carries value", raw: invalidState},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeRunRecord(json.RawMessage(test.raw)); CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestRecordDecodersRejectInvalidUUIDTimeJSONAndArrays(t *testing.T) {
	now := time.Now().UTC()
	validRun := RunRecord{
		ID: recordUUID1, WorkflowID: recordUUID2, Mode: "test", Status: "running", Input: json.RawMessage(`{}`),
		DraftRevision: pointer(int64(1)), GraphSnapshot: presentJSONB(`{}`), StartedAt: now, InputRedactedPaths: []string{},
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
		DraftRevision: pointer(int64(1)), GraphSnapshot: presentJSONB(`{}`), StartedAt: time.Now().UTC(),
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
			record.GraphSnapshot = NullableJSONB{}
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

func presentJSONB(value string) NullableJSONB {
	return NullableJSONB{Valid: true, Value: json.RawMessage(value)}
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
