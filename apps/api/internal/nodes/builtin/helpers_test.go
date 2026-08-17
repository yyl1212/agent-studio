package builtin

import (
	"encoding/json"

	"agentstudio.local/api/internal/domain"
)

func domainRequest(config json.RawMessage, inputs map[string][]any) domain.NodeRequest {
	return domain.NodeRequest{Config: config, Inputs: inputs}
}
