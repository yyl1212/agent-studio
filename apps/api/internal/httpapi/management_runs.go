package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) listRunSummaries(writer http.ResponseWriter, request *http.Request) {
	input, err := parseRunSummaryRequest(request.URL.Query())
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	page, err := handler.dependencies.RunManagement.List(request.Context(), input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *handler) cancelRun(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	summary, err := handler.dependencies.RunManagement.Cancel(request.Context(), runID)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}

func (handler *handler) previewRunRetry(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	preview, err := handler.dependencies.RunManagement.RetryPreview(request.Context(), runID)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, preview)
}

func (handler *handler) retryRun(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	parsedKey, err := uuid.Parse(keys[0])
	if err != nil || parsedKey.String() != keys[0] {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	var body workflow.RunRetryRequest
	if err := decodeJSON(writer, request, &body); err != nil || body.SecretValues == nil || !validRetrySecretValues(body.SecretValues) {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	submitted, err := handler.dependencies.RetrySubmissions.SubmitRetry(request.Context(), runID, keys[0], body)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	handler.streamSubmittedRun(writer, request, submitted)
}

func validRetrySecretValues(values map[string]any) bool {
	if len(values) > 256 {
		return false
	}
	for path := range values {
		if len(path) > 1024 {
			return false
		}
	}
	return true
}

func parseRunSummaryRequest(values url.Values) (workflow.RunSummaryRequest, error) {
	allowed := map[string]bool{
		"workflowId": true, "runId": true, "status": true, "mode": true,
		"startedAfter": true, "startedBefore": true, "cursor": true, "limit": true,
	}
	for key := range values {
		if !allowed[key] {
			return workflow.RunSummaryRequest{}, errInvalidManagementRequest
		}
	}
	for _, key := range []string{"workflowId", "runId", "startedAfter", "startedBefore", "cursor", "limit"} {
		if !hasAtMostOne(values, key) {
			return workflow.RunSummaryRequest{}, errInvalidManagementRequest
		}
	}
	if len(values["status"]) > 5 || len(values["mode"]) > 3 || len([]byte(values.Get("cursor"))) > 512 {
		return workflow.RunSummaryRequest{}, errInvalidManagementRequest
	}
	input := workflow.RunSummaryRequest{Cursor: values.Get("cursor")}
	var err error
	if input.WorkflowID, err = parseOptionalQueryUUID(values.Get("workflowId")); err != nil {
		return workflow.RunSummaryRequest{}, err
	}
	if input.RunID, err = parseOptionalQueryUUID(values.Get("runId")); err != nil {
		return workflow.RunSummaryRequest{}, err
	}
	for _, raw := range values["status"] {
		status := domain.RunStatus(raw)
		if status != domain.RunRunning && status != domain.RunCancelling && status != domain.RunCompleted && status != domain.RunFailed && status != domain.RunCancelled {
			return workflow.RunSummaryRequest{}, errInvalidManagementRequest
		}
		input.Statuses = append(input.Statuses, status)
	}
	for _, raw := range values["mode"] {
		mode := domain.RunMode(raw)
		if mode != domain.RunModeTest && mode != domain.RunModePublished && mode != domain.RunModeDebug {
			return workflow.RunSummaryRequest{}, errInvalidManagementRequest
		}
		input.Modes = append(input.Modes, mode)
	}
	if input.StartedAfter, err = parseOptionalQueryTime(values.Get("startedAfter")); err != nil {
		return workflow.RunSummaryRequest{}, err
	}
	if input.StartedBefore, err = parseOptionalQueryTime(values.Get("startedBefore")); err != nil {
		return workflow.RunSummaryRequest{}, err
	}
	if input.StartedAfter != nil && input.StartedBefore != nil && (!input.StartedBefore.After(*input.StartedAfter) || input.StartedBefore.Sub(*input.StartedAfter) > 90*24*time.Hour) {
		return workflow.RunSummaryRequest{}, errInvalidManagementRequest
	}
	if values.Has("limit") {
		input.Limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || input.Limit < 1 || input.Limit > 100 {
			return workflow.RunSummaryRequest{}, errInvalidManagementRequest
		}
	}
	return input, nil
}

func parseOptionalQueryUUID(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", errInvalidManagementRequest
	}
	return parsed.String(), nil
}

func parseOptionalQueryTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, errInvalidManagementRequest
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
