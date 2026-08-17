package builtin

import (
	"encoding/json"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func domainRequest(config json.RawMessage, inputs map[string][]any) domain.NodeRequest {
	return domain.NodeRequest{Config: config, Inputs: inputs}
}
