package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	validateWorkflowOne = "00000000-0000-0000-0000-000000000601"
	validateWorkflowTwo = "00000000-0000-0000-0000-000000000602"
	validateVersionOne  = "00000000-0000-0000-0000-000000000701"
	validateVersionTwo  = "00000000-0000-0000-0000-000000000702"
	validateRunOne      = "00000000-0000-0000-0000-000000000801"
	validateRunTwo      = "00000000-0000-0000-0000-000000000802"
	validateRunThree    = "00000000-0000-0000-0000-000000000803"
	validateNodeOne     = "00000000-0000-0000-0000-000000000901"
	validateRetryKey    = "00000000-0000-0000-0000-000000000a01"
)

func TestStageReferencesAcceptsValidArchive(t *testing.T) {
	archive := openReferenceFixtureArchive(t, nil)
	defer archive.Close()
	tx := beginReferenceRollbackTransaction(t)
	counts, err := stageReferences(context.Background(), tx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (ReferenceCounts{Workflows: 2, WorkflowVersions: 2, Runs: 3, NodeRuns: 1, RunEvents: 1, WorkflowDraftCheckpoints: 1}) {
		t.Fatalf("counts=%+v", counts)
	}
}

func TestStageReferencesRejectsInvalidRelationships(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*referenceFixtureData)
	}{
		{"duplicate workflow", duplicateReferenceWorkflowID},
		{"missing workflow version", removeReferencePublishedVersion},
		{"cross workflow published version", crossReferenceWorkflowPublishedVersion},
		{"missing source run", removeReferenceSourceRun},
		{"cross workflow retry", crossReferenceWorkflowRetry},
		{"cyclic run parents", cycleReferenceRunParents},
		{"child before parent", reorderReferenceChildBeforeParent},
		{"missing node run parent", removeReferenceNodeRunParent},
		{"missing event parent", removeReferenceEventParent},
		{"checkpoint version mismatch", crossReferenceWorkflowCheckpointVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := openReferenceFixtureArchive(t, test.mutate)
			defer archive.Close()
			tx := beginReferenceRollbackTransaction(t)
			_, err := stageReferences(context.Background(), tx, archive)
			if CodeOf(err) != CodeReferenceInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestStageReferencesRejectsRunLocalSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*referenceFixtureData)
	}{
		{"published source fields", invalidPublishedReferenceSourceFields},
		{"test workflow version", invalidTestReferenceWorkflowVersion},
		{"debug missing source", invalidDebugReferenceSource},
		{"unpaired retry fields", invalidReferenceRetryPair},
		{"agent key outside published mode", invalidReferenceAgentRequestKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := openReferenceFixtureArchive(t, test.mutate)
			defer archive.Close()
			if _, err := Inspect(context.Background(), archive.Summary().Path); err != nil {
				t.Fatalf("inspect err=%v", err)
			}
			_, err := stageReferences(context.Background(), beginReferenceRollbackTransaction(t), archive)
			if CodeOf(err) != CodeReferenceInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

func TestCopyReferenceTablePreservesCancellationMapsFailuresAndStopsProducer(t *testing.T) {
	tests := []struct {
		name   string
		ctx    func() (context.Context, func())
		result error
		want   Code
		isCtx  bool
	}{
		{
			name: "cancellation", ctx: func() (context.Context, func()) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			}, result: context.Canceled, isCtx: true,
		},
		{name: "runtime failure", ctx: normalReferenceContext, result: errors.New("sensitive postgres failure"), want: CodeRestoreFailed},
		{name: "constraint failure", ctx: normalReferenceContext, result: &pgconn.PgError{Code: "23505"}, want: CodeReferenceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := openReferenceFixtureArchive(t, nil)
			defer archive.Close()
			ctx, cancel := test.ctx()
			defer cancel()
			transaction := &copyFromStubTx{result: test.result}
			_, err := copyReferenceTable(ctx, transaction, archive, TableWorkflows)
			if test.isCtx {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			} else if CodeOf(err) != test.want {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
			if strings.Contains(err.Error(), "sensitive postgres failure") {
				t.Fatalf("unsafe error=%v", err)
			}
			if transaction.source == nil {
				t.Fatal("copy source was not used")
			}
			select {
			case <-transaction.source.done:
			default:
				t.Fatal("producer did not stop after CopyFrom returned")
			}
		})
	}
}

func normalReferenceContext() (context.Context, func()) { return context.Background(), func() {} }

type copyFromStubTx struct {
	pgx.Tx
	result error
	source *referenceCopySource
}

func (transaction *copyFromStubTx) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, source pgx.CopyFromSource) (int64, error) {
	transaction.source = source.(*referenceCopySource)
	transaction.source.Next()
	return 0, transaction.result
}

func TestStrictRecordDecodersRejectArchiveOnlyInvalidValues(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	workflow := WorkflowRecord{
		ID: validateWorkflowOne, Name: "One", Slug: "one", Description: "", DraftGraph: json.RawMessage(`{}`), DraftRevision: 1,
		CreatedAt: now, UpdatedAt: now, AgentPresentation: json.RawMessage(`{}`),
	}
	run := RunRecord{
		ID: validateRunOne, WorkflowID: validateWorkflowOne, DraftRevision: pointer(int64(1)), GraphSnapshot: presentJSONB(`{}`),
		Mode: "test", Status: "completed", Input: json.RawMessage(`{}`), InputRedactedPaths: []string{}, StartedAt: now,
	}
	workflowBody, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	runBody, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	missingID := strings.Replace(string(workflowBody), `"id":"`+validateWorkflowOne+`",`, "", 1)
	invalidTime := strings.Replace(string(runBody), `"startedAt":"2026-08-29T10:00:00Z"`, `"startedAt":"not-rfc3339"`, 1)
	for _, test := range []struct {
		name   string
		decode func(json.RawMessage) error
		body   json.RawMessage
	}{
		{"unknown field", func(raw json.RawMessage) error { _, err := decodeWorkflowRecord(raw); return err }, append(workflowBody[:len(workflowBody)-1], []byte(`,"unknown":true}`)...)},
		{"missing required uuid", func(raw json.RawMessage) error { _, err := decodeWorkflowRecord(raw); return err }, json.RawMessage(missingID)},
		{"invalid uuid", func(raw json.RawMessage) error { _, err := decodeRunRecord(raw); return err }, json.RawMessage(strings.Replace(string(runBody), validateRunOne, "not-a-uuid", 1))},
		{"invalid rfc3339 time", func(raw json.RawMessage) error { _, err := decodeRunRecord(raw); return err }, json.RawMessage(invalidTime)},
		{"non-object jsonb", func(raw json.RawMessage) error { _, err := decodeRunRecord(raw); return err }, json.RawMessage(strings.Replace(string(runBody), `"input":{}`, `"input":[]`, 1))},
		{"nil array", func(raw json.RawMessage) error { _, err := decodeRunRecord(raw); return err }, json.RawMessage(strings.Replace(string(runBody), `"inputRedactedPaths":[]`, `"inputRedactedPaths":null`, 1))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.decode(test.body); CodeOf(err) != CodeArchiveInvalid {
				t.Fatalf("code=%q err=%v", CodeOf(err), err)
			}
		})
	}
}

type referenceFixtureData struct {
	workflows   []WorkflowRecord
	versions    []WorkflowVersionRecord
	runs        []RunRecord
	nodeRuns    []NodeRunRecord
	runEvents   []RunEventRecord
	checkpoints []WorkflowDraftCheckpointRecord
}

func openReferenceFixtureArchive(t *testing.T, mutate func(*referenceFixtureData)) *Archive {
	t.Helper()
	data := newReferenceFixtureData()
	if mutate != nil {
		mutate(&data)
	}
	output := filepath.Join(t.TempDir(), "references.asbak")
	if _, err := WriteArchive(context.Background(), output, manifestFixture(time.Now().UTC()), referenceFixtureWriters(t, data)); err != nil {
		t.Fatal(err)
	}
	archive, err := OpenArchive(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

func beginReferenceRollbackTransaction(t *testing.T) pgx.Tx {
	t.Helper()
	pool := openBackupPool(t)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func newReferenceFixtureData() referenceFixtureData {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	workflowOneVersion := validateVersionOne
	workflowTwoVersion := validateVersionTwo
	runOne := validateRunOne
	return referenceFixtureData{
		workflows: []WorkflowRecord{
			{ID: validateWorkflowOne, Name: "One", Slug: "one", Description: "", DraftGraph: json.RawMessage(`{}`), DraftRevision: 1, PublishedVersionID: &workflowOneVersion, CreatedAt: now, UpdatedAt: now, AgentPresentation: json.RawMessage(`{}`)},
			{ID: validateWorkflowTwo, Name: "Two", Slug: "two", Description: "", DraftGraph: json.RawMessage(`{}`), DraftRevision: 1, PublishedVersionID: &workflowTwoVersion, CreatedAt: now, UpdatedAt: now, AgentPresentation: json.RawMessage(`{}`)},
		},
		versions: []WorkflowVersionRecord{
			{ID: validateVersionOne, WorkflowID: validateWorkflowOne, Version: 1, Graph: json.RawMessage(`{}`), InputSchema: json.RawMessage(`{}`), CreatedAt: now, AgentPresentation: json.RawMessage(`{}`)},
			{ID: validateVersionTwo, WorkflowID: validateWorkflowTwo, Version: 1, Graph: json.RawMessage(`{}`), InputSchema: json.RawMessage(`{}`), CreatedAt: now, AgentPresentation: json.RawMessage(`{}`)},
		},
		runs: []RunRecord{
			{ID: validateRunOne, WorkflowID: validateWorkflowOne, WorkflowVersionID: pointer(validateVersionOne), Mode: "published", Status: "completed", Input: json.RawMessage(`{}`), InputRedactedPaths: []string{}, StartedAt: now},
			{ID: validateRunTwo, WorkflowID: validateWorkflowOne, GraphSnapshot: presentJSONB(`{}`), Mode: "debug", Status: "completed", Input: json.RawMessage(`{}`), SourceRunID: &runOne, SourceNodeID: pointer("start"), InputRedactedPaths: []string{}, StartedAt: now.Add(time.Second)},
			{ID: validateRunThree, WorkflowID: validateWorkflowOne, WorkflowVersionID: pointer(validateVersionOne), Mode: "published", Status: "completed", Input: json.RawMessage(`{}`), RetryOfRunID: &runOne, RetryKey: pointer(validateRetryKey), InputRedactedPaths: []string{}, StartedAt: now.Add(2 * time.Second)},
		},
		nodeRuns:    []NodeRunRecord{{ID: validateNodeOne, RunID: validateRunTwo, NodeID: "start", NodeType: "start", Status: "completed"}},
		runEvents:   []RunEventRecord{{RunID: validateRunTwo, Sequence: 1, Type: "run.started", ActivePorts: []string{}, InputRedactedPaths: []string{}, OutputRedactedPaths: []string{}, Timestamp: now}},
		checkpoints: []WorkflowDraftCheckpointRecord{{WorkflowID: validateWorkflowOne, SourceRevision: 1, RestoredRevision: 2, Graph: json.RawMessage(`{}`), AgentPresentation: json.RawMessage(`{}`), RestoredFromVersionID: validateVersionOne, CreatedAt: now}},
	}
}

func referenceFixtureWriters(t *testing.T, data referenceFixtureData) map[TableName]TableWriter {
	t.Helper()
	items := map[TableName]any{
		TableWorkflows: data.workflows, TableWorkflowVersions: data.versions, TableRuns: data.runs,
		TableNodeRuns: data.nodeRuns, TableRunEvents: data.runEvents, TableWorkflowDraftCheckpoints: data.checkpoints,
	}
	writers := make(map[TableName]TableWriter, len(TableOrder))
	for _, name := range TableOrder {
		body := referenceFixtureJSONL(t, items[name])
		path, _ := tablePath(name)
		writers[name] = func(_ context.Context, writer io.Writer) (TableManifest, error) {
			if _, err := writer.Write(body); err != nil {
				return TableManifest{}, err
			}
			return TableManifest{Name: name, Path: path, Records: uint64(referenceFixtureRecordCount(items[name])), UncompressedBytes: uint64(len(body)), Digest: referenceFixtureDigest(body)}, nil
		}
	}
	return writers
}

func referenceFixtureJSONL(t *testing.T, records any) []byte {
	t.Helper()
	value := reflect.ValueOf(records)
	var body []byte
	for index := range value.Len() {
		record, err := json.Marshal(value.Index(index).Interface())
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, record...)
		body = append(body, '\n')
	}
	return body
}

func referenceFixtureRecordCount(records any) int { return reflect.ValueOf(records).Len() }

func referenceFixtureDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return digestPrefix + hex.EncodeToString(sum[:])
}

func duplicateReferenceWorkflowID(data *referenceFixtureData) {
	data.workflows = append(data.workflows, data.workflows[0])
}

func removeReferencePublishedVersion(data *referenceFixtureData) {
	missing := "00000000-0000-0000-0000-000000000799"
	data.workflows[0].PublishedVersionID = &missing
}

func crossReferenceWorkflowPublishedVersion(data *referenceFixtureData) {
	data.workflows[0].PublishedVersionID = pointer(validateVersionTwo)
}

func removeReferenceSourceRun(data *referenceFixtureData) {
	missing := "00000000-0000-0000-0000-000000000899"
	data.runs[1].SourceRunID = &missing
}

func crossReferenceWorkflowRetry(data *referenceFixtureData) {
	data.runs[2].WorkflowID = validateWorkflowTwo
	data.runs[2].WorkflowVersionID = pointer(validateVersionTwo)
}

func cycleReferenceRunParents(data *referenceFixtureData) {
	data.runs[0].RetryOfRunID = pointer(validateRunThree)
	data.runs[0].RetryKey = pointer(validateRetryKey)
}

func reorderReferenceChildBeforeParent(data *referenceFixtureData) {
	data.runs[0], data.runs[1] = data.runs[1], data.runs[0]
}

func removeReferenceNodeRunParent(data *referenceFixtureData) {
	data.nodeRuns[0].RunID = "00000000-0000-0000-0000-000000000899"
}

func removeReferenceEventParent(data *referenceFixtureData) {
	data.runEvents[0].RunID = "00000000-0000-0000-0000-000000000899"
}

func crossReferenceWorkflowCheckpointVersion(data *referenceFixtureData) {
	data.checkpoints[0].RestoredFromVersionID = validateVersionTwo
}

func invalidPublishedReferenceSourceFields(data *referenceFixtureData) {
	data.runs[0].SourceRunID = pointer(validateRunTwo)
	data.runs[0].SourceNodeID = pointer("start")
}

func invalidTestReferenceWorkflowVersion(data *referenceFixtureData) {
	data.runs[1].Mode = "test"
	data.runs[1].WorkflowVersionID = pointer(validateVersionOne)
	data.runs[1].DraftRevision = pointer(int64(1))
	data.runs[1].SourceRunID = nil
	data.runs[1].SourceNodeID = nil
}

func invalidDebugReferenceSource(data *referenceFixtureData) {
	data.runs[1].SourceRunID = nil
}

func invalidReferenceRetryPair(data *referenceFixtureData) {
	data.runs[2].RetryKey = nil
}

func invalidReferenceAgentRequestKey(data *referenceFixtureData) {
	data.runs[1].AgentRequestKey = pointer(validateRetryKey)
}
