package httpapi

import (
	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type nodeTypeResponse struct {
	agentnode.Definition
	Capabilities []agentnode.Capability `json:"capabilities"`
	Package      nodepackage.Summary    `json:"package"`
}

func nodeTypeResponses(entries []nodes.CatalogEntry) []nodeTypeResponse {
	responses := make([]nodeTypeResponse, 0, len(entries))
	for _, entry := range entries {
		definition := entry.Definition
		if definition.Inputs == nil {
			definition.Inputs = []domain.PortDefinition{}
		}
		if definition.Outputs == nil {
			definition.Outputs = []domain.PortDefinition{}
		}
		if definition.Capabilities == nil {
			definition.Capabilities = []agentnode.Capability{}
		}
		responses = append(responses, nodeTypeResponse{
			Definition: definition, Capabilities: definition.Capabilities, Package: entry.Package,
		})
	}
	return responses
}
