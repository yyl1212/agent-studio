package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func (handler *handler) listNodeTypes(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, nodeTypeResponses(handler.dependencies.Registry.Catalog()))
}

func (handler *handler) resolveNodeType(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Config json.RawMessage `json:"config"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	nodeType := chi.URLParam(request, "type")
	version := chi.URLParam(request, "version")
	if err := handler.dependencies.Registry.ValidateConfig(nodeType, version, body.Config); err != nil {
		writeRequestError(writer, request, err)
		return
	}
	node, err := handler.dependencies.Registry.Get(nodeType, version)
	if err != nil {
		writeError(writer, request, domain.ErrNotFound)
		return
	}
	ports, err := node.Resolve(body.Config)
	if err != nil {
		writeRequestError(writer, request, err)
		return
	}
	if ports.Inputs == nil {
		ports.Inputs = []domain.PortDefinition{}
	}
	if ports.Outputs == nil {
		ports.Outputs = []domain.PortDefinition{}
	}
	writeJSON(writer, http.StatusOK, ports)
}
