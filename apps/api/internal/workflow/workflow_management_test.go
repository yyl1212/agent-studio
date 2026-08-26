package workflow

import (
	"context"
	"encoding/json"
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

func TestWorkflowManagementUpdateValidatesAndNormalizesMetadata(t *testing.T) {
	store := &fakeWorkflowManagementStore{workflow: domain.Workflow{ID: "workflow-1", Name: "Old"}}
	service := NewWorkflowManagementService(store)
	updated, err := service.Update(context.Background(), store.workflow.ID, UpdateWorkflowInput{
		Name: "  新名称  ", Description: strings.Repeat("界", 2048),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "新名称" || store.updatedName != "新名称" || len([]rune(store.updatedDescription)) != 2048 || store.updateCalls != 1 {
		t.Fatalf("updated=%+v store=%+v", updated, store)
	}

	invalid := []UpdateWorkflowInput{
		{Name: "   "},
		{Name: strings.Repeat("界", 129)},
		{Name: "valid", Description: strings.Repeat("界", 2049)},
	}
	for _, input := range invalid {
		if _, err := service.Update(context.Background(), store.workflow.ID, input); !errors.Is(err, ErrInvalidWorkflowInput) {
			t.Fatalf("input=%+v error=%v", input, err)
		}
	}
	if store.updateCalls != 1 {
		t.Fatalf("invalid inputs wrote metadata: calls=%d", store.updateCalls)
	}
}

func TestWorkflowManagementUpdateRejectsArchivedWorkflowBeforeWrite(t *testing.T) {
	archivedAt := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	store := &fakeWorkflowManagementStore{workflow: domain.Workflow{ID: "workflow-1", ArchivedAt: &archivedAt}}
	_, err := NewWorkflowManagementService(store).Update(context.Background(), store.workflow.ID, UpdateWorkflowInput{Name: "禁止修改"})
	if !errors.Is(err, domain.ErrWorkflowArchived) {
		t.Fatalf("error=%v", err)
	}
	if store.updateCalls != 0 {
		t.Fatalf("archived workflow reached metadata write: calls=%d", store.updateCalls)
	}
}

func TestWorkflowManagementCopyClonesOnlyArchivedSourceDraft(t *testing.T) {
	archivedAt := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	publishedID := "version-1"
	publishedVersion := 7
	store := &fakeWorkflowManagementStore{workflow: domain.Workflow{
		ID: "workflow-1", Name: "Source", Slug: "source", Description: "源说明",
		DraftGraph: json.RawMessage(`{"schemaVersion":1,"nodes":[],"edges":[]}`), DraftRevision: 9,
		AgentPresentation:  domain.AgentPresentation{Title: "源页面", Description: "页面说明", Accent: domain.AgentAccentAmber, SubmitLabel: "执行", ResultMode: domain.AgentResultModeJSON},
		PublishedVersionID: &publishedID, PublishedVersion: &publishedVersion, ArchivedAt: &archivedAt,
	}}
	service := NewWorkflowManagementService(store)
	created, err := service.Copy(context.Background(), store.workflow.ID, CopyWorkflowInput{Name: "  副本  ", Slug: "copy-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == store.workflow.ID || created.Name != "副本" || created.Slug != "copy-agent" || created.Description != store.workflow.Description || created.DraftRevision != 1 {
		t.Fatalf("created=%+v", created)
	}
	if created.PublishedVersionID != nil || created.PublishedVersion != nil || created.ArchivedAt != nil || store.createCalls != 1 {
		t.Fatalf("created lifecycle=%+v calls=%d", created, store.createCalls)
	}
	if created.AgentPresentation != store.workflow.AgentPresentation {
		t.Fatalf("copy presentation=%+v source=%+v", created.AgentPresentation, store.workflow.AgentPresentation)
	}
	created.DraftGraph[0] = 'x'
	if store.workflow.DraftGraph[0] == 'x' {
		t.Fatal("copied draft aliases source")
	}

	if _, err := service.Copy(context.Background(), store.workflow.ID, CopyWorkflowInput{Name: "Copy", Slug: "Bad Slug"}); !errors.Is(err, ErrInvalidWorkflowInput) {
		t.Fatalf("invalid copy error=%v", err)
	}
	if store.createCalls != 1 {
		t.Fatalf("invalid copy wrote workflow: calls=%d", store.createCalls)
	}
}

func TestWorkflowManagementArchiveAndRestoreDelegateIdempotently(t *testing.T) {
	store := &fakeWorkflowManagementStore{workflow: domain.Workflow{ID: "workflow-1"}}
	service := NewWorkflowManagementService(store)
	firstArchive, err := service.Archive(context.Background(), store.workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondArchive, err := service.Archive(context.Background(), store.workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstArchive.ArchivedAt == nil || secondArchive.ArchivedAt == nil || !firstArchive.ArchivedAt.Equal(*secondArchive.ArchivedAt) {
		t.Fatalf("archives=%+v %+v", firstArchive, secondArchive)
	}
	firstRestore, err := service.Restore(context.Background(), store.workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondRestore, err := service.Restore(context.Background(), store.workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstRestore.ArchivedAt != nil || secondRestore.ArchivedAt != nil || store.archiveTransitions != 1 || store.restoreTransitions != 1 {
		t.Fatalf("restores=%+v %+v transitions=%d/%d", firstRestore, secondRestore, store.archiveTransitions, store.restoreTransitions)
	}
}

type fakeWorkflowManagementStore struct {
	summaries          []domain.WorkflowSummary
	workflow           domain.Workflow
	query              WorkflowSummaryStoreQuery
	err                error
	calls              int
	createCalls        int
	updateCalls        int
	updatedName        string
	updatedDescription string
	archiveTransitions int
	restoreTransitions int
}

func (store *fakeWorkflowManagementStore) ListWorkflowSummaries(_ context.Context, query WorkflowSummaryStoreQuery) ([]domain.WorkflowSummary, error) {
	store.calls++
	store.query = query
	return store.summaries, store.err
}

func (store *fakeWorkflowManagementStore) GetWorkflow(_ context.Context, id string) (domain.Workflow, error) {
	if id != store.workflow.ID {
		return domain.Workflow{}, domain.ErrNotFound
	}
	return store.workflow, nil
}

func (store *fakeWorkflowManagementStore) CreateWorkflow(_ context.Context, value domain.Workflow) (domain.Workflow, error) {
	store.createCalls++
	if store.err != nil {
		return domain.Workflow{}, store.err
	}
	return value, nil
}

func (store *fakeWorkflowManagementStore) UpdateWorkflowMetadata(_ context.Context, id, name, description string) (domain.Workflow, error) {
	store.updateCalls++
	store.updatedName = name
	store.updatedDescription = description
	store.workflow.Name = name
	store.workflow.Description = description
	return store.workflow, nil
}

func (store *fakeWorkflowManagementStore) ArchiveWorkflow(_ context.Context, id string) (domain.Workflow, error) {
	if store.workflow.ArchivedAt == nil {
		archivedAt := time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC)
		store.workflow.ArchivedAt = &archivedAt
		store.archiveTransitions++
	}
	return store.workflow, nil
}

func (store *fakeWorkflowManagementStore) RestoreWorkflow(_ context.Context, id string) (domain.Workflow, error) {
	if store.workflow.ArchivedAt != nil {
		store.workflow.ArchivedAt = nil
		store.restoreTransitions++
	}
	return store.workflow, nil
}
