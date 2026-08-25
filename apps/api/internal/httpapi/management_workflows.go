package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

var errInvalidManagementRequest = errors.New("invalid management request")

func (handler *handler) listWorkflowSummaries(writer http.ResponseWriter, request *http.Request) {
	input, err := parseWorkflowSummaryRequest(request.URL.Query())
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	page, err := handler.dependencies.WorkflowManagement.List(request.Context(), input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *handler) updateWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var body struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decodeJSON(writer, request, &body); err != nil || body.Name == nil || body.Description == nil || strings.TrimSpace(*body.Name) == "" {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	updated, err := handler.dependencies.WorkflowManagement.Update(request.Context(), id, workflow.UpdateWorkflowInput{Name: *body.Name, Description: *body.Description})
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func (handler *handler) copyWorkflow(writer http.ResponseWriter, request *http.Request) {
	id, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var body struct {
		Name *string `json:"name"`
		Slug *string `json:"slug"`
	}
	if err := decodeJSON(writer, request, &body); err != nil || body.Name == nil || body.Slug == nil || strings.TrimSpace(*body.Name) == "" || *body.Slug == "" {
		writeRequestError(writer, request, errInvalidManagementRequest)
		return
	}
	created, err := handler.dependencies.WorkflowManagement.Copy(request.Context(), id, workflow.CopyWorkflowInput{Name: *body.Name, Slug: *body.Slug})
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, created)
}

func (handler *handler) archiveWorkflow(writer http.ResponseWriter, request *http.Request) {
	handler.changeWorkflowArchiveState(writer, request, true)
}

func (handler *handler) restoreWorkflow(writer http.ResponseWriter, request *http.Request) {
	handler.changeWorkflowArchiveState(writer, request, false)
}

func (handler *handler) changeWorkflowArchiveState(writer http.ResponseWriter, request *http.Request, archive bool) {
	id, err := parsePathUUID(request, "id")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var updated any
	if archive {
		updated, err = handler.dependencies.WorkflowManagement.Archive(request.Context(), id)
	} else {
		updated, err = handler.dependencies.WorkflowManagement.Restore(request.Context(), id)
	}
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func parseWorkflowSummaryRequest(values url.Values) (workflow.WorkflowSummaryRequest, error) {
	allowed := map[string]bool{"q": true, "state": true, "cursor": true, "limit": true}
	for key := range values {
		if !allowed[key] || !hasAtMostOne(values, key) {
			return workflow.WorkflowSummaryRequest{}, errInvalidManagementRequest
		}
	}
	input := workflow.WorkflowSummaryRequest{Text: values.Get("q"), State: workflow.WorkflowState(values.Get("state")), Cursor: values.Get("cursor")}
	if !utf8.ValidString(input.Text) || len([]byte(input.Text)) > 100 || len([]byte(input.Cursor)) > 512 {
		return workflow.WorkflowSummaryRequest{}, errInvalidManagementRequest
	}
	if input.State != "" && input.State != workflow.WorkflowStateActive && input.State != workflow.WorkflowStateArchived && input.State != workflow.WorkflowStateAll {
		return workflow.WorkflowSummaryRequest{}, errInvalidManagementRequest
	}
	if values.Has("limit") {
		limit, err := strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			return workflow.WorkflowSummaryRequest{}, errInvalidManagementRequest
		}
		input.Limit = limit
	}
	return input, nil
}

func parsePathUUID(request *http.Request, parameter string) (string, error) {
	parsed, err := uuid.Parse(chi.URLParam(request, parameter))
	if err != nil {
		return "", errInvalidManagementRequest
	}
	return parsed.String(), nil
}
