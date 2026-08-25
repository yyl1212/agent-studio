package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestWorkflowManagementListAppliesDefaultsAndReturnsEmptyArray(t *testing.T) {
	store := &fakeWorkflowManagementStore{}
	service := NewWorkflowManagementService(store)
	page, err := service.List(context.Background(), WorkflowSummaryRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("page=%+v", page)
	}
	if store.query.Text != "" || store.query.State != WorkflowStateActive || store.query.Limit != 51 {
		t.Fatalf("query=%+v", store.query)
	}
}

func TestWorkflowManagementListNormalizesAndPaginatesWithoutAliasing(t *testing.T) {
	firstTime := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)
	secondTime := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	publishedVersionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	publishedVersion := 2
	archivedAt := firstTime
	store := &fakeWorkflowManagementStore{summaries: []domain.WorkflowSummary{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "A", PublishedVersionID: &publishedVersionID, PublishedVersion: &publishedVersion, ArchivedAt: &archivedAt, UpdatedAt: firstTime},
		{ID: "22222222-2222-4222-8222-222222222222", Name: "B", UpdatedAt: secondTime},
	}}
	service := NewWorkflowManagementService(store)
	page, err := service.List(context.Background(), WorkflowSummaryRequest{
		Text: "  Agent  ", State: WorkflowStateAll, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != store.summaries[0].ID || page.NextCursor == nil {
		t.Fatalf("page=%+v", page)
	}
	if store.query.Text != "Agent" || store.query.State != WorkflowStateAll || store.query.Limit != 2 {
		t.Fatalf("query=%+v", store.query)
	}
	page.Items[0].Name = "changed"
	*page.Items[0].PublishedVersionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	*page.Items[0].PublishedVersion = 3
	*page.Items[0].ArchivedAt = secondTime
	if store.summaries[0].Name != "A" || *store.summaries[0].PublishedVersionID != publishedVersionID || *store.summaries[0].PublishedVersion != publishedVersion || !store.summaries[0].ArchivedAt.Equal(archivedAt) {
		t.Fatalf("store slice aliased: %+v", store.summaries)
	}

	nextStore := &fakeWorkflowManagementStore{}
	nextPage, err := NewWorkflowManagementService(nextStore).List(context.Background(), WorkflowSummaryRequest{
		Text: "Agent", State: WorkflowStateAll, Limit: 1, Cursor: *page.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextPage.Items == nil || nextStore.query.AfterUpdated == nil || !nextStore.query.AfterUpdated.Equal(firstTime) || nextStore.query.AfterID != store.summaries[0].ID {
		t.Fatalf("next page=%+v query=%+v", nextPage, nextStore.query)
	}
}

func TestWorkflowManagementListRejectsInvalidRequestBeforeStore(t *testing.T) {
	validCursor, err := encodePageCursor(
		time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC),
		"11111111-1111-4111-8111-111111111111",
		filterFingerprint(workflowSummaryFilter{Query: "different", State: WorkflowStateActive}),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		request WorkflowSummaryRequest
		want    error
	}{
		{name: "raw text bytes", request: WorkflowSummaryRequest{Text: strings.Repeat(" ", 101)}, want: ErrInvalidWorkflowInput},
		{name: "unknown state", request: WorkflowSummaryRequest{State: "deleted"}, want: ErrInvalidWorkflowInput},
		{name: "negative limit", request: WorkflowSummaryRequest{Limit: -1}, want: ErrInvalidWorkflowInput},
		{name: "limit too large", request: WorkflowSummaryRequest{Limit: 101}, want: ErrInvalidWorkflowInput},
		{name: "cursor filter mismatch", request: WorkflowSummaryRequest{Cursor: validCursor}, want: ErrCursorInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeWorkflowManagementStore{}
			_, err := NewWorkflowManagementService(store).List(context.Background(), test.request)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v, want %v", err, test.want)
			}
			if store.calls != 0 {
				t.Fatalf("store calls=%d", store.calls)
			}
		})
	}
}

func TestWorkflowManagementListPropagatesStoreError(t *testing.T) {
	want := errors.New("store unavailable")
	store := &fakeWorkflowManagementStore{err: want}
	_, err := NewWorkflowManagementService(store).List(context.Background(), WorkflowSummaryRequest{})
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
}

type fakeWorkflowManagementStore struct {
	summaries []domain.WorkflowSummary
	query     WorkflowSummaryStoreQuery
	err       error
	calls     int
}

func (store *fakeWorkflowManagementStore) ListWorkflowSummaries(_ context.Context, query WorkflowSummaryStoreQuery) ([]domain.WorkflowSummary, error) {
	store.calls++
	store.query = query
	return store.summaries, store.err
}
