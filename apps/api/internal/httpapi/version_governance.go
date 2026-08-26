package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) listWorkflowVersions(writer http.ResponseWriter, request *http.Request) {
	id, err := parseCanonicalPathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	input, err := parseWorkflowVersionListRequest(request.URL.Query())
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	page, err := handler.dependencies.VersionGovernance.List(request.Context(), id, input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *handler) diffWorkflowVersions(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	id, err := parseCanonicalPathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var input workflow.WorkflowDiffRequest
	if err := decodeJSON(writer, request, &input); err != nil || !validWorkflowSnapshotRef(input.Base) || !validWorkflowSnapshotRef(input.Compare) {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	diff, err := handler.dependencies.VersionGovernance.Diff(request.Context(), id, input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, diff)
}

func (handler *handler) rollbackWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := parseCanonicalPathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var input workflow.WorkflowRollbackInput
	if err := decodeJSON(writer, request, &input); err != nil || input.TargetVersion <= 0 || input.ExpectedDraftRevision <= 0 {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	result, err := handler.dependencies.VersionGovernance.Rollback(request.Context(), id, input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *handler) undoWorkflowRollback(writer http.ResponseWriter, request *http.Request) {
	id, err := parseCanonicalPathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var input workflow.WorkflowRollbackUndoInput
	if err := decodeJSON(writer, request, &input); err != nil || input.ExpectedDraftRevision <= 0 {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	updated, err := handler.dependencies.VersionGovernance.Undo(request.Context(), id, input.ExpectedDraftRevision)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func parseWorkflowVersionListRequest(values url.Values) (workflow.WorkflowVersionListRequest, error) {
	for key := range values {
		if (key != "cursor" && key != "limit") || !hasAtMostOne(values, key) {
			return workflow.WorkflowVersionListRequest{}, errInvalidManagementRequest
		}
	}
	request := workflow.WorkflowVersionListRequest{Cursor: values.Get("cursor"), Limit: 20}
	if !utf8.ValidString(request.Cursor) || len([]byte(request.Cursor)) > 512 {
		return workflow.WorkflowVersionListRequest{}, errInvalidManagementRequest
	}
	if values.Has("limit") {
		limit, err := strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			return workflow.WorkflowVersionListRequest{}, errInvalidManagementRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func validWorkflowSnapshotRef(ref domain.WorkflowSnapshotRef) bool {
	switch ref.Kind {
	case domain.WorkflowSnapshotDraft:
		return ref.DraftRevision != nil && *ref.DraftRevision > 0 && ref.Version == nil
	case domain.WorkflowSnapshotVersion:
		return ref.Version != nil && *ref.Version > 0 && ref.DraftRevision == nil
	default:
		return false
	}
}

func parseCanonicalPathUUID(request *http.Request, parameter string) (string, error) {
	raw := chi.URLParam(request, parameter)
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed.String() != raw {
		return "", errInvalidManagementRequest
	}
	return raw, nil
}
