package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const maxRunFilterSpan = 90 * 24 * time.Hour

var ErrRunNotCancellable = errors.New("run is not cancellable")

type runSummaryFilter struct {
	WorkflowID    string             `json:"workflowId,omitempty"`
	Statuses      []domain.RunStatus `json:"statuses,omitempty"`
	Modes         []domain.RunMode   `json:"modes,omitempty"`
	StartedAfter  *time.Time         `json:"startedAfter,omitempty"`
	StartedBefore *time.Time         `json:"startedBefore,omitempty"`
	RunID         string             `json:"runId,omitempty"`
}

type RunManagementService struct {
	store     RunManagementStore
	compiler  Compiler
	canceller LocalRunCanceller
}

func NewRunManagementService(store RunManagementStore, compiler Compiler, canceller LocalRunCanceller) *RunManagementService {
	return &RunManagementService{store: store, compiler: compiler, canceller: canceller}
}

func (service *RunManagementService) Cancel(ctx context.Context, runID string) (domain.RunSummary, error) {
	normalized, err := normalizeOptionalUUID(runID)
	if err != nil || normalized == "" {
		return domain.RunSummary{}, ErrInvalidWorkflowInput
	}
	summary, err := service.store.RequestRunCancel(ctx, normalized)
	if err != nil {
		return domain.RunSummary{}, fmt.Errorf("request run cancel: %w", err)
	}
	if service.canceller != nil {
		service.canceller.CancelLocal(normalized)
	}
	return cloneRunSummary(summary), nil
}

func (service *RunManagementService) List(ctx context.Context, request RunSummaryRequest) (RunSummaryPage, error) {
	workflowID, err := normalizeOptionalUUID(request.WorkflowID)
	if err != nil {
		return RunSummaryPage{}, err
	}
	runID, err := normalizeOptionalUUID(request.RunID)
	if err != nil {
		return RunSummaryPage{}, err
	}
	statuses, err := normalizeRunStatuses(request.Statuses)
	if err != nil {
		return RunSummaryPage{}, err
	}
	modes, err := normalizeRunModes(request.Modes)
	if err != nil {
		return RunSummaryPage{}, err
	}
	startedAfter := normalizeOptionalTime(request.StartedAfter)
	startedBefore := normalizeOptionalTime(request.StartedBefore)
	if startedAfter != nil && startedBefore != nil && (!startedBefore.After(*startedAfter) || startedBefore.Sub(*startedAfter) > maxRunFilterSpan) {
		return RunSummaryPage{}, ErrInvalidWorkflowInput
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultManagementPageLimit
	}
	if limit < 1 || limit > maxManagementPageLimit {
		return RunSummaryPage{}, ErrInvalidWorkflowInput
	}

	filter := runSummaryFilter{
		WorkflowID: workflowID, Statuses: statuses, Modes: modes,
		StartedAfter: startedAfter, StartedBefore: startedBefore, RunID: runID,
	}
	fingerprint := filterFingerprint(filter)
	storeQuery := RunSummaryStoreQuery{
		WorkflowID: workflowID, Statuses: append([]domain.RunStatus(nil), statuses...), Modes: append([]domain.RunMode(nil), modes...),
		StartedAfter: normalizeOptionalTime(startedAfter), StartedBefore: normalizeOptionalTime(startedBefore), RunID: runID, Limit: limit + 1,
	}
	if request.Cursor != "" {
		afterStarted, afterID, err := decodePageCursor(request.Cursor, fingerprint)
		if err != nil {
			return RunSummaryPage{}, err
		}
		storeQuery.AfterStarted = &afterStarted
		storeQuery.AfterID = afterID
	}
	rows, err := service.store.ListRunSummaries(ctx, storeQuery)
	if err != nil {
		return RunSummaryPage{}, fmt.Errorf("list run summaries: %w", err)
	}
	visible := len(rows)
	if visible > limit {
		visible = limit
	}
	items := make([]domain.RunSummary, visible)
	for index := range visible {
		items[index] = cloneRunSummary(rows[index])
	}
	page := RunSummaryPage{Items: items}
	if len(rows) > limit {
		last := items[len(items)-1]
		nextCursor, err := encodePageCursor(last.StartedAt, last.ID, fingerprint)
		if err != nil {
			return RunSummaryPage{}, err
		}
		page.NextCursor = &nextCursor
	}
	return page, nil
}

func normalizeOptionalUUID(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", ErrInvalidWorkflowInput
	}
	return parsed.String(), nil
}

func normalizeRunStatuses(values []domain.RunStatus) ([]domain.RunStatus, error) {
	if len(values) > 5 {
		return nil, ErrInvalidWorkflowInput
	}
	result := append([]domain.RunStatus(nil), values...)
	for _, value := range result {
		if value != domain.RunRunning && value != domain.RunCancelling && value != domain.RunCompleted && value != domain.RunFailed && value != domain.RunCancelled {
			return nil, ErrInvalidWorkflowInput
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return deduplicateRunStatuses(result), nil
}

func normalizeRunModes(values []domain.RunMode) ([]domain.RunMode, error) {
	if len(values) > 3 {
		return nil, ErrInvalidWorkflowInput
	}
	result := append([]domain.RunMode(nil), values...)
	for _, value := range result {
		if value != domain.RunModeTest && value != domain.RunModePublished && value != domain.RunModeDebug {
			return nil, ErrInvalidWorkflowInput
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return deduplicateRunModes(result), nil
}

func deduplicateRunStatuses(values []domain.RunStatus) []domain.RunStatus {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func deduplicateRunModes(values []domain.RunMode) []domain.RunMode {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func normalizeOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func cloneRunSummary(value domain.RunSummary) domain.RunSummary {
	cloned := value
	cloned.WorkflowVersionID = cloneStringPointer(value.WorkflowVersionID)
	cloned.WorkflowVersion = cloneIntPointer(value.WorkflowVersion)
	cloned.DraftRevision = cloneInt64Pointer(value.DraftRevision)
	cloned.SourceRunID = cloneStringPointer(value.SourceRunID)
	cloned.SourceNodeID = cloneStringPointer(value.SourceNodeID)
	cloned.RetryOfRunID = cloneStringPointer(value.RetryOfRunID)
	if value.CancelRequestedAt != nil {
		cancelRequestedAt := *value.CancelRequestedAt
		cloned.CancelRequestedAt = &cancelRequestedAt
	}
	if value.EndedAt != nil {
		endedAt := *value.EndedAt
		cloned.EndedAt = &endedAt
	}
	return cloned
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
