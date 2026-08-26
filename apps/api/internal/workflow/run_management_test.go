package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestHistoricPlaceholderPathsEscapesSortsAndEnforcesBudgets(t *testing.T) {
	input := map[string]any{
		"token":    redactedValue,
		"nested":   []any{map[string]any{"a/b~c": redactedValue}},
		"ordinary": "visible",
	}
	paths, err := historicRedactedPaths(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/nested/0/a~1b~0c", "/token"}) {
		t.Fatalf("paths=%v", paths)
	}

	tooMany := make(map[string]any, 257)
	for index := 0; index < 257; index++ {
		tooMany[string(rune(0x1000+index))] = redactedValue
	}
	if _, err := historicRedactedPaths(tooMany); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("too many paths error=%v", err)
	}
	if _, err := historicRedactedPaths(map[string]any{strings.Repeat("x", 1025): redactedValue}); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("long path error=%v", err)
	}
	deep := any(redactedValue)
	for range 129 {
		deep = []any{deep}
	}
	if _, err := historicRedactedPaths(deep); !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("deep input error=%v", err)
	}
}

func TestApplyRetrySecretsUsesExactStrictPointersWithoutMutation(t *testing.T) {
	input := map[string]any{
		"token":  redactedValue,
		"nested": []any{map[string]any{"a/b~c": redactedValue}},
		"keep":   "visible",
	}
	wantOriginal, _ := json.Marshal(input)
	replaced, err := applyRetrySecrets(input,
		[]string{"/nested/0/a~1b~0c", "/token"},
		map[string]any{"/token": "new-token", "/nested/0/a~1b~0c": false},
	)
	if err != nil {
		t.Fatal(err)
	}
	if replaced["token"] != "new-token" || replaced["keep"] != "visible" {
		t.Fatalf("replaced=%v", replaced)
	}
	nested := replaced["nested"].([]any)[0].(map[string]any)
	if nested["a/b~c"] != false {
		t.Fatalf("escaped replacement=%v", nested)
	}
	gotOriginal, _ := json.Marshal(input)
	if string(gotOriginal) != string(wantOriginal) {
		t.Fatalf("source mutated: before=%s after=%s", wantOriginal, gotOriginal)
	}
}

func TestApplyRetrySecretsRejectsNonExactInvalidEmptyAndResidualValues(t *testing.T) {
	input := map[string]any{"token": redactedValue, "nested": []any{redactedValue}}
	tests := []struct {
		name     string
		required []string
		provided map[string]any
	}{
		{name: "missing", required: []string{"/token"}, provided: map[string]any{}},
		{name: "extra", required: []string{"/token"}, provided: map[string]any{"/token": "ok", "/extra": "no"}},
		{name: "duplicate", required: []string{"/token", "/token"}, provided: map[string]any{"/token": "ok"}},
		{name: "bad escape", required: []string{"/bad~2key"}, provided: map[string]any{"/bad~2key": "ok"}},
		{name: "append", required: []string{"/nested/-"}, provided: map[string]any{"/nested/-": "ok"}},
		{name: "missing target", required: []string{"/missing"}, provided: map[string]any{"/missing": "ok"}},
		{name: "null", required: []string{"/token"}, provided: map[string]any{"/token": nil}},
		{name: "empty", required: []string{"/token"}, provided: map[string]any{"/token": ""}},
		{name: "placeholder", required: []string{"/token"}, provided: map[string]any{"/token": redactedValue}},
		{name: "residual", required: []string{"/token"}, provided: map[string]any{"/token": "ok"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := applyRetrySecrets(input, test.required, test.provided); !errors.Is(err, ErrRunRetrySecretRequired) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestRetryPreviewUsesHistoricTestAndPublishedGraphs(t *testing.T) {
	publishedVersionID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	for _, test := range []struct {
		name    string
		mode    domain.RunMode
		status  domain.RunStatus
		version *string
	}{
		{name: "failed test", mode: domain.RunModeTest, status: domain.RunFailed},
		{name: "cancelled published", mode: domain.RunModePublished, status: domain.RunCancelled, version: &publishedVersionID},
	} {
		t.Run(test.name, func(t *testing.T) {
			historicGraph := graphReturningField(t, "historic-", "historic")
			workflowRecord := domain.Workflow{ID: testWorkflowID, Name: "Workflow", Slug: "workflow", DraftGraph: graphReturningField(t, "current-", "current")}
			store := &fakeRunManagementStore{
				run: domain.Run{
					ID: testRunID, WorkflowID: testWorkflowID, WorkflowVersionID: test.version,
					Mode: test.mode, Status: test.status, GraphSnapshot: historicGraph,
					Input:              json.RawMessage(`{"historic":"visible","token":"[REDACTED]","nested":[{"a/b~c":"[REDACTED]"}]}`),
					InputRedactedPaths: []string{"/token", "/token"},
				},
				workflow: workflowRecord,
				version:  domain.WorkflowVersion{ID: publishedVersionID, WorkflowID: testWorkflowID, Version: 7, Graph: historicGraph},
			}
			preview, err := NewRunManagementService(store, newRealCompiler(t), nil).RetryPreview(context.Background(), testRunID)
			if err != nil {
				t.Fatal(err)
			}
			if preview.RetryOfRunID != testRunID || preview.Source.ID != testRunID || preview.Source.WorkflowName != "Workflow" || preview.Source.WorkflowSlug != "workflow" {
				t.Fatalf("preview source=%+v retryOf=%s", preview.Source, preview.RetryOfRunID)
			}
			if !reflect.DeepEqual(preview.InputRedactedPaths, []string{"/nested/0/a~1b~0c", "/token"}) || preview.Input["historic"] != "visible" {
				t.Fatalf("input=%v paths=%v", preview.Input, preview.InputRedactedPaths)
			}
			if !strings.Contains(string(preview.InputSchema), `"historic"`) || strings.Contains(string(preview.InputSchema), `"current"`) {
				t.Fatalf("schema=%s", preview.InputSchema)
			}
			if test.mode == domain.RunModePublished && store.versionID != publishedVersionID {
				t.Fatalf("loaded version=%s", store.versionID)
			}
		})
	}
}

func TestRetryPreviewRejectsIneligibleAndArchivedRuns(t *testing.T) {
	for _, test := range []struct {
		name   string
		mode   domain.RunMode
		status domain.RunStatus
	}{
		{name: "running", mode: domain.RunModeTest, status: domain.RunRunning},
		{name: "cancelling", mode: domain.RunModeTest, status: domain.RunCancelling},
		{name: "completed", mode: domain.RunModeTest, status: domain.RunCompleted},
		{name: "debug", mode: domain.RunModeDebug, status: domain.RunFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeRunManagementStore{
				run:      domain.Run{ID: testRunID, WorkflowID: testWorkflowID, Mode: test.mode, Status: test.status},
				workflow: domain.Workflow{ID: testWorkflowID},
			}
			_, err := NewRunManagementService(store, newRealCompiler(t), nil).RetryPreview(context.Background(), testRunID)
			if !errors.Is(err, ErrRunNotRetryable) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	archivedAt := time.Now().UTC()
	store := &fakeRunManagementStore{
		run:      domain.Run{ID: testRunID, WorkflowID: testWorkflowID, Mode: domain.RunModeTest, Status: domain.RunFailed},
		workflow: domain.Workflow{ID: testWorkflowID, ArchivedAt: &archivedAt},
	}
	_, err := NewRunManagementService(store, newRealCompiler(t), nil).RetryPreview(context.Background(), testRunID)
	if !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("archived error=%v", err)
	}
}

func TestRetryPreviewRejectsOversizedInput(t *testing.T) {
	graph := graphReturningField(t, "historic-", "historic")
	input, err := json.Marshal(map[string]any{"historic": strings.Repeat("x", 1<<20)})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeRunManagementStore{
		run:      domain.Run{ID: testRunID, WorkflowID: testWorkflowID, Mode: domain.RunModeTest, Status: domain.RunFailed, GraphSnapshot: graph, Input: input},
		workflow: domain.Workflow{ID: testWorkflowID},
	}
	_, err = NewRunManagementService(store, newRealCompiler(t), nil).RetryPreview(context.Background(), testRunID)
	if !errors.Is(err, ErrRunNotRetryable) {
		t.Fatalf("error=%v", err)
	}
}

func TestPrepareRetryRestoresSecretsValidatesAndCreatesHistoricRun(t *testing.T) {
	graph := rerunGraph(t)
	store := &fakeRunManagementStore{
		run: domain.Run{
			ID: testRunID, WorkflowID: testWorkflowID, Mode: domain.RunModeTest, Status: domain.RunFailed,
			GraphSnapshot: graph, DraftRevision: int64Pointer(3),
			Input: json.RawMessage(`{"seed":"visible","webhookToken":"[REDACTED]"}`), InputRedactedPaths: []string{"/webhookToken"},
		},
		workflow: domain.Workflow{ID: testWorkflowID, Name: "Workflow", Slug: "workflow"},
	}
	key := "33333333-3333-4333-8333-333333333333"
	prepared, err := NewRunManagementService(store, newRealCompiler(t), nil).PrepareRetry(context.Background(), testRunID, key, RunRetryRequest{
		SecretValues: map[string]any{"/webhookToken": "new-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RunID == "" || prepared.Mode != domain.RunModeTest || prepared.Input["webhookToken"] != "new-secret" || prepared.Input["seed"] != "visible" {
		t.Fatalf("prepared=%+v", prepared)
	}
	created := store.retryRun
	if created.RetryOfRunID == nil || *created.RetryOfRunID != testRunID || created.RetryKey == nil || *created.RetryKey != key || created.SourceRunID != nil || created.Mode != domain.RunModeTest {
		t.Fatalf("created=%+v", created)
	}
	if string(created.GraphSnapshot) != string(graph) || strings.Contains(string(created.Input), "new-secret") || !strings.Contains(string(created.Input), redactedValue) {
		t.Fatalf("snapshot=%s persisted input=%s", created.GraphSnapshot, created.Input)
	}
}

func TestPrepareRetryRejectsInvalidKeyAndSecretSetBeforeCreate(t *testing.T) {
	graph := rerunGraph(t)
	newStore := func() *fakeRunManagementStore {
		return &fakeRunManagementStore{
			run: domain.Run{ID: testRunID, WorkflowID: testWorkflowID, Mode: domain.RunModeTest, Status: domain.RunFailed, GraphSnapshot: graph,
				Input: json.RawMessage(`{"seed":"visible","webhookToken":"[REDACTED]"}`), InputRedactedPaths: []string{"/webhookToken"}},
			workflow: domain.Workflow{ID: testWorkflowID},
		}
	}
	for _, test := range []struct {
		name string
		key  string
		body RunRetryRequest
		want error
	}{
		{name: "invalid key", key: "bad", body: RunRetryRequest{SecretValues: map[string]any{"/webhookToken": "secret"}}, want: ErrInvalidWorkflowInput},
		{name: "non canonical key", key: "33333333-3333-4333-8333-333333333333 ", body: RunRetryRequest{SecretValues: map[string]any{"/webhookToken": "secret"}}, want: ErrInvalidWorkflowInput},
		{name: "missing secret", key: "33333333-3333-4333-8333-333333333333", body: RunRetryRequest{SecretValues: map[string]any{}}, want: ErrRunRetrySecretRequired},
		{name: "schema mismatch", key: "33333333-3333-4333-8333-333333333333", body: RunRetryRequest{SecretValues: map[string]any{"/webhookToken": false}}, want: ErrInputValidation},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newStore()
			_, err := NewRunManagementService(store, newRealCompiler(t), nil).PrepareRetry(context.Background(), testRunID, test.key, test.body)
			if !errors.Is(err, test.want) || store.retryCalls != 0 {
				t.Fatalf("error=%v retry calls=%d", err, store.retryCalls)
			}
		})
	}
}

func TestPrepareRetryUsesOriginalPublishedVersionAndRechecksEligibility(t *testing.T) {
	versionID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	graph := rerunGraph(t)
	store := &fakeRunManagementStore{
		run: domain.Run{ID: testRunID, WorkflowID: testWorkflowID, WorkflowVersionID: &versionID, Mode: domain.RunModePublished, Status: domain.RunCancelled,
			Input: json.RawMessage(`{"seed":"visible"}`)},
		workflow: domain.Workflow{ID: testWorkflowID, Slug: "workflow", DraftGraph: graphReturningField(t, "current-", "current")},
		version:  domain.WorkflowVersion{ID: versionID, WorkflowID: testWorkflowID, Version: 4, Graph: graph},
	}
	prepared, err := NewRunManagementService(store, newRealCompiler(t), nil).PrepareRetry(context.Background(), testRunID,
		"33333333-3333-4333-8333-333333333333", RunRetryRequest{SecretValues: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Mode != domain.RunModePublished || prepared.WorkflowVersionID == nil || *prepared.WorkflowVersionID != versionID || store.retryRun.GraphSnapshot != nil || store.versionID != versionID {
		t.Fatalf("prepared=%+v created=%+v loaded version=%s", prepared, store.retryRun, store.versionID)
	}

	store.run.Status = domain.RunRunning
	store.retryCalls = 0
	_, err = NewRunManagementService(store, newRealCompiler(t), nil).PrepareRetry(context.Background(), testRunID,
		"44444444-4444-4444-8444-444444444444", RunRetryRequest{SecretValues: map[string]any{}})
	if !errors.Is(err, ErrRunNotRetryable) || store.retryCalls != 0 {
		t.Fatalf("error=%v retry calls=%d", err, store.retryCalls)
	}
}

func int64Pointer(value int64) *int64 { return &value }

const (
	testWorkflowID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testRunID      = "11111111-1111-4111-8111-111111111111"
)

func TestRunManagementListAppliesDefaultsWithoutImplicitTimeRange(t *testing.T) {
	store := &fakeRunManagementStore{}
	page, err := NewRunManagementService(store, nil, nil).List(context.Background(), RunSummaryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("page=%+v", page)
	}
	if store.query.Limit != 51 || store.query.StartedAfter != nil || store.query.StartedBefore != nil {
		t.Fatalf("query=%+v", store.query)
	}
}

func TestRunManagementListNormalizesFiltersAndPaginatesWithoutMutation(t *testing.T) {
	startedAfter := time.Date(2026, 8, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	startedBefore := time.Date(2026, 8, 25, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	statuses := []domain.RunStatus{domain.RunFailed, domain.RunRunning, domain.RunFailed}
	modes := []domain.RunMode{domain.RunModeTest, domain.RunModeDebug, domain.RunModeTest}
	wantStatuses := append([]domain.RunStatus(nil), statuses...)
	wantModes := append([]domain.RunMode(nil), modes...)
	firstTime := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	secondTime := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	versionID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	version := 2
	store := &fakeRunManagementStore{summaries: []domain.RunSummary{
		{ID: testRunID, WorkflowID: testWorkflowID, WorkflowVersionID: &versionID, WorkflowVersion: &version, StartedAt: firstTime},
		{ID: "22222222-2222-4222-8222-222222222222", WorkflowID: testWorkflowID, StartedAt: secondTime},
	}}
	page, err := NewRunManagementService(store, nil, nil).List(context.Background(), RunSummaryRequest{
		WorkflowID: testWorkflowID, Statuses: statuses, Modes: modes,
		StartedAfter: &startedAfter, StartedBefore: &startedBefore, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(statuses, wantStatuses) || !reflect.DeepEqual(modes, wantModes) {
		t.Fatalf("caller filters mutated: statuses=%v modes=%v", statuses, modes)
	}
	if !reflect.DeepEqual(store.query.Statuses, []domain.RunStatus{domain.RunFailed, domain.RunRunning}) || !reflect.DeepEqual(store.query.Modes, []domain.RunMode{domain.RunModeDebug, domain.RunModeTest}) {
		t.Fatalf("normalized query=%+v", store.query)
	}
	if store.query.StartedAfter == nil || store.query.StartedAfter.Location() != time.UTC || store.query.StartedBefore == nil || store.query.StartedBefore.Location() != time.UTC || store.query.Limit != 2 {
		t.Fatalf("query=%+v", store.query)
	}
	if len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("page=%+v", page)
	}
	*page.Items[0].WorkflowVersionID = "changed"
	*page.Items[0].WorkflowVersion = 3
	if *store.summaries[0].WorkflowVersionID != versionID || *store.summaries[0].WorkflowVersion != version {
		t.Fatal("run summary pointers aliased store")
	}

	nextStore := &fakeRunManagementStore{}
	nextPage, err := NewRunManagementService(nextStore, nil, nil).List(context.Background(), RunSummaryRequest{
		WorkflowID:    testWorkflowID,
		Statuses:      []domain.RunStatus{domain.RunRunning, domain.RunFailed},
		Modes:         []domain.RunMode{domain.RunModeTest, domain.RunModeDebug},
		StartedAfter:  &startedAfter,
		StartedBefore: &startedBefore,
		Limit:         1,
		Cursor:        *page.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextPage.Items == nil || nextStore.query.AfterStarted == nil || !nextStore.query.AfterStarted.Equal(firstTime) || nextStore.query.AfterID != testRunID {
		t.Fatalf("next page=%+v query=%+v", nextPage, nextStore.query)
	}
}

func TestRunManagementListRejectsInvalidFiltersBeforeStore(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tooLate := after.Add(90*24*time.Hour + time.Nanosecond)
	beforeAfter := after.Add(-time.Second)
	tests := []struct {
		name    string
		request RunSummaryRequest
	}{
		{name: "workflow uuid", request: RunSummaryRequest{WorkflowID: "bad"}},
		{name: "run uuid", request: RunSummaryRequest{RunID: "bad"}},
		{name: "sixth status", request: RunSummaryRequest{Statuses: []domain.RunStatus{domain.RunRunning, domain.RunCancelling, domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunFailed}}},
		{name: "unknown status", request: RunSummaryRequest{Statuses: []domain.RunStatus{"paused"}}},
		{name: "fourth mode", request: RunSummaryRequest{Modes: []domain.RunMode{domain.RunModeTest, domain.RunModePublished, domain.RunModeDebug, domain.RunModeTest}}},
		{name: "unknown mode", request: RunSummaryRequest{Modes: []domain.RunMode{"batch"}}},
		{name: "time order", request: RunSummaryRequest{StartedAfter: &after, StartedBefore: &beforeAfter}},
		{name: "time span", request: RunSummaryRequest{StartedAfter: &after, StartedBefore: &tooLate}},
		{name: "negative limit", request: RunSummaryRequest{Limit: -1}},
		{name: "large limit", request: RunSummaryRequest{Limit: 101}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeRunManagementStore{}
			_, err := NewRunManagementService(store, nil, nil).List(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidWorkflowInput) || store.calls != 0 {
				t.Fatalf("error=%v calls=%d", err, store.calls)
			}
		})
	}
}

func TestCancelRunNormalizesIDAndCancelsLocalExecution(t *testing.T) {
	store := &fakeRunManagementStore{cancelSummary: domain.RunSummary{ID: testRunID, Status: domain.RunCancelling}}
	canceller := &fakeLocalRunCanceller{}
	service := NewRunManagementService(store, nil, canceller)
	summary, err := service.Cancel(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != domain.RunCancelling || store.cancelRunID != testRunID || !reflect.DeepEqual(canceller.ids, []string{testRunID}) {
		t.Fatalf("summary=%+v store ID=%s local IDs=%v", summary, store.cancelRunID, canceller.ids)
	}
}

func TestCancelRunRejectsInvalidIDBeforeStore(t *testing.T) {
	store := &fakeRunManagementStore{}
	canceller := &fakeLocalRunCanceller{}
	_, err := NewRunManagementService(store, nil, canceller).Cancel(context.Background(), "bad")
	if !errors.Is(err, ErrInvalidWorkflowInput) || store.cancelCalls != 0 || len(canceller.ids) != 0 {
		t.Fatalf("error=%v store calls=%d local IDs=%v", err, store.cancelCalls, canceller.ids)
	}
}

func TestCancelRunKeepsTerminalRunsUnchanged(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunCompleted, domain.RunFailed, domain.RunCancelled} {
		t.Run(string(status), func(t *testing.T) {
			store := &fakeRunManagementStore{cancelErr: ErrRunNotCancellable}
			canceller := &fakeLocalRunCanceller{}
			_, err := NewRunManagementService(store, nil, canceller).Cancel(context.Background(), testRunID)
			if !errors.Is(err, ErrRunNotCancellable) || len(canceller.ids) != 0 {
				t.Fatalf("status=%s error=%v local IDs=%v", status, err, canceller.ids)
			}
		})
	}
}

func TestRunManagementListRejectsCursorFromDifferentFilter(t *testing.T) {
	cursor, err := encodePageCursor(
		time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC),
		testRunID,
		filterFingerprint(runSummaryFilter{WorkflowID: testWorkflowID}),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeRunManagementStore{}
	_, err = NewRunManagementService(store, nil, nil).List(context.Background(), RunSummaryRequest{RunID: testRunID, Cursor: cursor})
	if !errors.Is(err, ErrCursorInvalid) || store.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, store.calls)
	}
}

type fakeRunManagementStore struct {
	summaries     []domain.RunSummary
	query         RunSummaryStoreQuery
	err           error
	calls         int
	cancelSummary domain.RunSummary
	cancelErr     error
	cancelRunID   string
	cancelCalls   int
	run           domain.Run
	runErr        error
	workflow      domain.Workflow
	workflowErr   error
	version       domain.WorkflowVersion
	versionErr    error
	versionID     string
	retryRun      domain.Run
	retryErr      error
	retryCalls    int
}

func (store *fakeRunManagementStore) CreateRetryRun(_ context.Context, run domain.Run) (string, error) {
	store.retryCalls++
	store.retryRun = run
	return run.ID, store.retryErr
}

func (store *fakeRunManagementStore) RequestRunCancel(_ context.Context, runID string) (domain.RunSummary, error) {
	store.cancelCalls++
	store.cancelRunID = runID
	return store.cancelSummary, store.cancelErr
}

func (store *fakeRunManagementStore) GetRun(_ context.Context, runID string) (domain.Run, []domain.NodeRun, error) {
	if store.runErr != nil {
		return domain.Run{}, nil, store.runErr
	}
	if store.run.ID == "" || store.run.ID != runID {
		return domain.Run{}, nil, domain.ErrNotFound
	}
	return store.run, nil, nil
}

func (store *fakeRunManagementStore) GetWorkflow(_ context.Context, workflowID string) (domain.Workflow, error) {
	if store.workflowErr != nil {
		return domain.Workflow{}, store.workflowErr
	}
	if store.workflow.ID == "" || store.workflow.ID != workflowID {
		return domain.Workflow{}, domain.ErrNotFound
	}
	return store.workflow, nil
}

func (store *fakeRunManagementStore) GetAgentVersion(_ context.Context, _ string, versionID string) (domain.Workflow, domain.WorkflowVersion, error) {
	store.versionID = versionID
	if store.versionErr != nil {
		return domain.Workflow{}, domain.WorkflowVersion{}, store.versionErr
	}
	if store.version.ID == "" || store.version.ID != versionID {
		return domain.Workflow{}, domain.WorkflowVersion{}, domain.ErrNotFound
	}
	return store.workflow, store.version, nil
}

type fakeLocalRunCanceller struct {
	ids []string
}

func (canceller *fakeLocalRunCanceller) CancelLocal(runID string) bool {
	canceller.ids = append(canceller.ids, runID)
	return true
}

func (store *fakeRunManagementStore) ListRunSummaries(_ context.Context, query RunSummaryStoreQuery) ([]domain.RunSummary, error) {
	store.calls++
	store.query = query
	return store.summaries, store.err
}
