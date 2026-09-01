package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) getAgentManifest(writer http.ResponseWriter, request *http.Request) {
	setAgentPublicHeaders(writer)
	manifest, err := handler.dependencies.Workflows.AgentManifest(request.Context(), chi.URLParam(request, "slug"))
	if err != nil {
		writeAgentError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, manifest)
}

func (handler *handler) runAgent(writer http.ResponseWriter, request *http.Request) {
	respondAsync := hasRespondAsyncPreference(request.Header.Values("Prefer"))
	if respondAsync {
		setAgentPublicHeaders(writer)
	}
	var body struct {
		WorkflowVersionID string         `json:"workflowVersionId"`
		Input             map[string]any `json:"input"`
	}
	if err := decodeJSON(writer, request, &body); err != nil || body.WorkflowVersionID == "" {
		writeRequestError(writer, request, err)
		return
	}
	if respondAsync {
		requestKeys := request.Header.Values("Idempotency-Key")
		requestKey, keyErr := parseAgentUUIDHeader(requestKeys)
		versionID, versionErr := parseAgentUUID(body.WorkflowVersionID)
		if keyErr != nil || versionErr != nil {
			writeRequestError(writer, request, errors.New("invalid asynchronous agent run identifier"))
			return
		}
		accepted, _, err := handler.dependencies.AgentRuns.Start(request.Context(), chi.URLParam(request, "slug"), workflow.StartAgentRunInput{
			WorkflowVersionID: versionID,
			RequestKey:        requestKey,
			Input:             body.Input,
		})
		if err != nil {
			writeAgentError(writer, request, err)
			return
		}
		writer.Header().Set("Preference-Applied", "respond-async")
		writeJSON(writer, http.StatusAccepted, accepted)
		return
	}
	submitted, err := handler.dependencies.RunSubmissions.SubmitAgent(request.Context(), chi.URLParam(request, "slug"), body.WorkflowVersionID, body.Input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	handler.streamSubmittedRun(writer, request, submitted)
}

func (handler *handler) getAgentRun(writer http.ResponseWriter, request *http.Request) {
	setAgentPublicHeaders(writer)
	runID, err := parseAgentUUID(chi.URLParam(request, "runID"))
	if err != nil {
		writeAgentError(writer, request, domain.ErrNotFound)
		return
	}
	afterSequence, err := parseAgentAfterSequence(request)
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	view, err := handler.dependencies.AgentRuns.View(request.Context(), chi.URLParam(request, "slug"), runID, afterSequence)
	if err != nil {
		writeAgentError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (handler *handler) cancelAgentRun(writer http.ResponseWriter, request *http.Request) {
	setAgentPublicHeaders(writer)
	runID, err := parseAgentUUID(chi.URLParam(request, "runID"))
	if err != nil {
		writeAgentError(writer, request, domain.ErrNotFound)
		return
	}
	if request.ContentLength > 0 {
		writeRequestError(writer, request, errors.New("agent run cancellation does not accept a body"))
		return
	}
	summary, err := handler.dependencies.AgentRuns.Cancel(request.Context(), chi.URLParam(request, "slug"), runID)
	if err != nil {
		writeAgentError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}

func hasRespondAsyncPreference(values []string) bool {
	for _, value := range values {
		for _, preference := range strings.Split(value, ",") {
			token := strings.TrimSpace(strings.SplitN(preference, ";", 2)[0])
			if strings.EqualFold(token, "respond-async") {
				return true
			}
		}
	}
	return false
}

func parseAgentUUIDHeader(values []string) (string, error) {
	if len(values) != 1 {
		return "", errors.New("exactly one idempotency key is required")
	}
	return parseAgentUUID(values[0])
}

func parseAgentUUID(raw string) (string, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func parseAgentAfterSequence(request *http.Request) (int64, error) {
	values := request.URL.Query()["afterSequence"]
	if len(values) == 0 {
		return 0, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, errors.New("invalid afterSequence")
	}
	sequence, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || sequence < 0 {
		return 0, errors.New("invalid afterSequence")
	}
	return sequence, nil
}

func setAgentPublicHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
}
