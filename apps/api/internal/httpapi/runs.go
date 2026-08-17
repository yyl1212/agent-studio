package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"agentstudio.local/api/internal/engine"
	"agentstudio.local/api/internal/workflow"
	"github.com/go-chi/chi/v5"
)

func (handler *handler) runDraft(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DraftRevision int64          `json:"draftRevision"`
		Input         map[string]any `json:"input"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	prepared, err := handler.dependencies.Runner.PrepareDraft(request.Context(), chi.URLParam(request, "id"), body.DraftRevision, body.Input)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	handler.streamRun(writer, request, prepared)
}

func (handler *handler) streamRun(writer http.ResponseWriter, request *http.Request, prepared *workflow.PreparedRun) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, request, fmt.Errorf("streaming is not supported"))
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	observer := &streamObserver{writer: writer, flusher: flusher}
	_, _ = handler.dependencies.Runner.Execute(request.Context(), prepared, observer)
}

type streamObserver struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (observer *streamObserver) Observe(_ context.Context, event engine.Event) error {
	if err := json.NewEncoder(observer.writer).Encode(event); err != nil {
		return err
	}
	observer.flusher.Flush()
	return nil
}

func (handler *handler) listRuns(writer http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeRequestError(writer, request, err)
			return
		}
		limit = parsed
	}
	runs, err := handler.dependencies.Runs.ListRuns(request.Context(), chi.URLParam(request, "id"), limit)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, runs)
}

func (handler *handler) getRun(writer http.ResponseWriter, request *http.Request) {
	run, nodeRuns, err := handler.dependencies.Runs.GetRun(request.Context(), chi.URLParam(request, "id"))
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"run": run, "nodeRuns": nodeRuns})
}
