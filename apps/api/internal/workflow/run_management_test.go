package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const (
	testWorkflowID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testRunID      = "11111111-1111-4111-8111-111111111111"
)

func TestRunManagementListAppliesDefaultsWithoutImplicitTimeRange(t *testing.T) {
	store := &fakeRunManagementStore{}
	page, err := NewRunManagementService(store).List(context.Background(), RunSummaryRequest{})
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
	page, err := NewRunManagementService(store).List(context.Background(), RunSummaryRequest{
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
	nextPage, err := NewRunManagementService(nextStore).List(context.Background(), RunSummaryRequest{
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
		{name: "fifth status", request: RunSummaryRequest{Statuses: []domain.RunStatus{domain.RunRunning, domain.RunCompleted, domain.RunFailed, domain.RunCancelled, domain.RunFailed}}},
		{name: "unknown status", request: RunSummaryRequest{Statuses: []domain.RunStatus{"cancelling"}}},
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
			_, err := NewRunManagementService(store).List(context.Background(), test.request)
			if !errors.Is(err, ErrInvalidWorkflowInput) || store.calls != 0 {
				t.Fatalf("error=%v calls=%d", err, store.calls)
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
	_, err = NewRunManagementService(store).List(context.Background(), RunSummaryRequest{RunID: testRunID, Cursor: cursor})
	if !errors.Is(err, ErrCursorInvalid) || store.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, store.calls)
	}
}

type fakeRunManagementStore struct {
	summaries []domain.RunSummary
	query     RunSummaryStoreQuery
	err       error
	calls     int
}

func (store *fakeRunManagementStore) ListRunSummaries(_ context.Context, query RunSummaryStoreQuery) ([]domain.RunSummary, error) {
	store.calls++
	store.query = query
	return store.summaries, store.err
}
