package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/workflow"
)

func (handler *handler) getRunRecovery(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "runId")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	view, err := handler.dependencies.RunRecovery.Get(request.Context(), runID)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (handler *handler) confirmRunNodeRetry(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "runId")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	nodeID := chi.URLParam(request, "nodeId")
	var body workflow.ConfirmNodeRetryRequest
	if nodeID == "" || decodeJSON(writer, request, &body) != nil || body.NodeAttempt < 1 || body.ExpectedSequence < 1 {
		writeRequestError(writer, request, workflow.ErrInvalidWorkflowInput)
		return
	}
	summary, err := handler.dependencies.RunRecovery.ConfirmNodeRetry(request.Context(), runID, nodeID, body)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}

func (handler *handler) terminateRunRecovery(writer http.ResponseWriter, request *http.Request) {
	runID, err := parsePathUUID(request, "runId")
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	var body workflow.TerminateRecoveryRequest
	if decodeJSON(writer, request, &body) != nil || body.ExpectedSequence < 1 {
		writeRequestError(writer, request, workflow.ErrInvalidWorkflowInput)
		return
	}
	summary, err := handler.dependencies.RunRecovery.Terminate(request.Context(), runID, body)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, summary)
}
