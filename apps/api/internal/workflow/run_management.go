package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const maxRunFilterSpan = 90 * 24 * time.Hour

const (
	maxRetryRedactedDepth = 128
	maxRetryRedactedPaths = 256
	maxRetryPointerBytes  = 1024
	maxRetryPreviewBytes  = 1 << 20
)

var (
	ErrRunNotCancellable      = errors.New("run is not cancellable")
	ErrRunNotRetryable        = errors.New("run not retryable")
	ErrRunRetrySecretRequired = errors.New("run retry secret required")
)

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

func (service *RunManagementService) RetryPreview(ctx context.Context, runID string) (RunRetryPreview, error) {
	normalized, err := normalizeOptionalUUID(runID)
	if err != nil || normalized == "" {
		return RunRetryPreview{}, ErrInvalidWorkflowInput
	}
	run, _, err := service.store.GetRun(ctx, normalized)
	if err != nil {
		return RunRetryPreview{}, fmt.Errorf("load retry source run: %w", err)
	}
	workflowRecord, err := service.store.GetWorkflow(ctx, run.WorkflowID)
	if err != nil {
		return RunRetryPreview{}, fmt.Errorf("load retry source workflow: %w", err)
	}
	if workflowRecord.ArchivedAt != nil {
		return RunRetryPreview{}, domain.ErrWorkflowArchived
	}
	if (run.Status != domain.RunFailed && run.Status != domain.RunCancelled) || (run.Mode != domain.RunModeTest && run.Mode != domain.RunModePublished) {
		return RunRetryPreview{}, ErrRunNotRetryable
	}
	_, graph, _, err := loadRunGraphData(ctx, service.store, service.compiler, run)
	if err != nil {
		return RunRetryPreview{}, err
	}
	inputSchema, err := deriveInputSchema(graph)
	if err != nil {
		return RunRetryPreview{}, ErrRunNotRetryable
	}
	var input map[string]any
	if err := decodeJSON(run.Input, &input); err != nil || input == nil {
		return RunRetryPreview{}, ErrRunNotRetryable
	}
	encodedInput, err := json.Marshal(input)
	if err != nil || len(encodedInput) > maxRetryPreviewBytes {
		return RunRetryPreview{}, ErrRunNotRetryable
	}
	paths, err := historicRedactedPaths(input)
	if err != nil {
		return RunRetryPreview{}, err
	}
	paths, err = mergeRetryRedactedPaths(paths, run.InputRedactedPaths)
	if err != nil {
		return RunRetryPreview{}, err
	}
	clonedInput, err := cloneJSONMap(input)
	if err != nil {
		return RunRetryPreview{}, ErrRunNotRetryable
	}
	return RunRetryPreview{
		Source:             runSummaryFromRun(run, workflowRecord),
		RetryOfRunID:       run.ID,
		Input:              clonedInput,
		InputRedactedPaths: paths,
		InputSchema:        append(json.RawMessage(nil), inputSchema...),
	}, nil
}

func (service *RunManagementService) PrepareRetry(ctx context.Context, sourceRunID, idempotencyKey string, request RunRetryRequest) (*PreparedRun, error) {
	normalizedSourceID, err := normalizeOptionalUUID(sourceRunID)
	if err != nil || normalizedSourceID == "" || !isCanonicalUUID(idempotencyKey) {
		return nil, ErrInvalidWorkflowInput
	}
	source, _, err := service.store.GetRun(ctx, normalizedSourceID)
	if err != nil {
		return nil, fmt.Errorf("load retry source run: %w", err)
	}
	workflowRecord, err := service.store.GetWorkflow(ctx, source.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("load retry source workflow: %w", err)
	}
	if workflowRecord.ArchivedAt != nil {
		return nil, domain.ErrWorkflowArchived
	}
	if (source.Status != domain.RunFailed && source.Status != domain.RunCancelled) || (source.Mode != domain.RunModeTest && source.Mode != domain.RunModePublished) {
		return nil, ErrRunNotRetryable
	}
	rawGraph, graph, plan, err := loadRunGraphData(ctx, service.store, service.compiler, source)
	if err != nil {
		return nil, err
	}
	inputSchema, err := deriveInputSchema(graph)
	if err != nil {
		return nil, ErrRunNotRetryable
	}
	var historicInput map[string]any
	if err := decodeJSON(source.Input, &historicInput); err != nil || historicInput == nil {
		return nil, ErrRunNotRetryable
	}
	discovered, err := historicRedactedPaths(historicInput)
	if err != nil {
		return nil, err
	}
	requiredPaths, err := mergeRetryRedactedPaths(discovered, source.InputRedactedPaths)
	if err != nil {
		return nil, err
	}
	input, err := applyRetrySecrets(historicInput, requiredPaths, request.SecretValues)
	if err != nil {
		return nil, err
	}
	if err := validateInput(inputSchema, input); err != nil {
		return nil, err
	}
	encodedInput, err := json.Marshal(input)
	if err != nil || len(encodedInput) > maxRetryPreviewBytes {
		return nil, ErrRunNotRetryable
	}
	persistedInput, persistedPaths, secretRedactor, err := persistedRunInput(input)
	if err != nil {
		return nil, fmt.Errorf("encode retry run input: %w", err)
	}
	runID := uuid.NewString()
	retryOfRunID, retryKey := source.ID, idempotencyKey
	created := domain.Run{
		ID: runID, WorkflowID: source.WorkflowID, Mode: source.Mode, Status: domain.RunRunning,
		RetryOfRunID: &retryOfRunID, RetryKey: &retryKey, Input: persistedInput, InputRedactedPaths: persistedPaths,
		StartedAt: time.Now().UTC(),
	}
	if source.Mode == domain.RunModeTest {
		created.DraftRevision = cloneInt64Pointer(source.DraftRevision)
		created.GraphSnapshot = append(json.RawMessage(nil), rawGraph...)
	} else {
		created.WorkflowVersionID = cloneStringPointer(source.WorkflowVersionID)
	}
	createdID, err := service.store.CreateRetryRun(ctx, created)
	if err != nil {
		return nil, err
	}
	return &PreparedRun{
		RunID: createdID, Plan: plan, Input: input, Mode: created.Mode, WorkflowID: created.WorkflowID,
		WorkflowVersionID: cloneStringPointer(created.WorkflowVersionID), DraftRevision: cloneInt64Pointer(created.DraftRevision),
		secretRedactor: secretRedactor, retryOfRunID: source.ID,
	}, nil
}

func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func mergeRetryRedactedPaths(discovered, persisted []string) ([]string, error) {
	unique := make(map[string]struct{}, len(discovered)+len(persisted))
	for _, path := range append(append([]string(nil), discovered...), persisted...) {
		if len(path) > maxRetryPointerBytes {
			return nil, ErrRunNotRetryable
		}
		if _, valid := decodeStrictJSONPointer(path); !valid || path == "" {
			return nil, ErrRunNotRetryable
		}
		unique[path] = struct{}{}
		if len(unique) > maxRetryRedactedPaths {
			return nil, ErrRunNotRetryable
		}
	}
	result := make([]string, 0, len(unique))
	for path := range unique {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func runSummaryFromRun(run domain.Run, workflowRecord domain.Workflow) domain.RunSummary {
	return domain.RunSummary{
		ID: run.ID, WorkflowID: run.WorkflowID, WorkflowName: workflowRecord.Name, WorkflowSlug: workflowRecord.Slug,
		WorkflowVersionID: cloneStringPointer(run.WorkflowVersionID), DraftRevision: cloneInt64Pointer(run.DraftRevision),
		SourceRunID: cloneStringPointer(run.SourceRunID), SourceNodeID: cloneStringPointer(run.SourceNodeID),
		RetryOfRunID: cloneStringPointer(run.RetryOfRunID), Mode: run.Mode, Status: run.Status,
		CancelRequestedAt: cloneTimePointer(run.CancelRequestedAt), StartedAt: run.StartedAt, EndedAt: cloneTimePointer(run.EndedAt),
	}
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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

func historicRedactedPaths(value any) ([]string, error) {
	paths := make([]string, 0)
	var scan func(any, string, int) error
	scan = func(current any, path string, depth int) error {
		if depth > maxRetryRedactedDepth {
			return ErrRunNotRetryable
		}
		if text, ok := current.(string); ok && text == redactedValue {
			if len(path) > maxRetryPointerBytes || len(paths) >= maxRetryRedactedPaths {
				return ErrRunNotRetryable
			}
			paths = append(paths, path)
			return nil
		}
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				childPath := jsonPointerChild(path, key)
				if len(childPath) > maxRetryPointerBytes {
					return ErrRunNotRetryable
				}
				if err := scan(child, childPath, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for index, child := range typed {
				childPath := jsonPointerChild(path, strconv.Itoa(index))
				if len(childPath) > maxRetryPointerBytes {
					return ErrRunNotRetryable
				}
				if err := scan(child, childPath, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := scan(value, "", 0); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func applyRetrySecrets(input map[string]any, required []string, provided map[string]any) (map[string]any, error) {
	if len(required) > maxRetryRedactedPaths || len(required) != len(provided) {
		return nil, ErrRunRetrySecretRequired
	}
	seen := make(map[string]struct{}, len(required))
	for _, path := range required {
		if _, exists := seen[path]; exists {
			return nil, ErrRunRetrySecretRequired
		}
		seen[path] = struct{}{}
		if _, exists := provided[path]; !exists {
			return nil, ErrRunRetrySecretRequired
		}
	}
	for path := range provided {
		if _, exists := seen[path]; !exists {
			return nil, ErrRunRetrySecretRequired
		}
	}

	cloned, err := cloneJSONMap(input)
	if err != nil {
		return nil, ErrRunRetrySecretRequired
	}
	for _, path := range required {
		tokens, ok := decodeStrictJSONPointer(path)
		if !ok || len(tokens) == 0 || len(path) > maxRetryPointerBytes {
			return nil, ErrRunRetrySecretRequired
		}
		replacement := provided[path]
		if replacement == nil || replacement == "" || replacement == redactedValue {
			return nil, ErrRunRetrySecretRequired
		}
		if !replaceRetryPointer(cloned, tokens, replacement) {
			return nil, ErrRunRetrySecretRequired
		}
	}
	remaining, err := historicRedactedPaths(cloned)
	if err != nil || len(remaining) > 0 {
		return nil, ErrRunRetrySecretRequired
	}
	return cloned, nil
}

func cloneJSONMap(value map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var cloned map[string]any
	if err := decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func decodeStrictJSONPointer(pointer string) ([]string, bool) {
	if pointer == "" {
		return []string{}, true
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	encoded := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(encoded))
	for index, token := range encoded {
		var decoded strings.Builder
		for offset := 0; offset < len(token); offset++ {
			if token[offset] != '~' {
				decoded.WriteByte(token[offset])
				continue
			}
			if offset+1 >= len(token) || (token[offset+1] != '0' && token[offset+1] != '1') {
				return nil, false
			}
			offset++
			if token[offset] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		tokens[index] = decoded.String()
	}
	return tokens, true
}

func replaceRetryPointer(root map[string]any, tokens []string, replacement any) bool {
	var current any = root
	for index, token := range tokens {
		last := index == len(tokens)-1
		switch typed := current.(type) {
		case map[string]any:
			value, exists := typed[token]
			if !exists {
				return false
			}
			if last {
				if value != redactedValue {
					return false
				}
				typed[token] = replacement
				return true
			}
			current = value
		case []any:
			if token == "-" || token == "" || (len(token) > 1 && token[0] == '0') {
				return false
			}
			arrayIndex, err := strconv.Atoi(token)
			if err != nil || arrayIndex < 0 || arrayIndex >= len(typed) {
				return false
			}
			if last {
				if typed[arrayIndex] != redactedValue {
					return false
				}
				typed[arrayIndex] = replacement
				return true
			}
			current = typed[arrayIndex]
		default:
			return false
		}
	}
	return false
}
