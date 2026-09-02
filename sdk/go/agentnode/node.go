package agentnode

import (
	"context"
	"encoding/json"
)

const APIVersion = "agent-studio.dev/v1alpha1"
const Version = "0.5.0"

type Node interface {
	Definition() Definition
	Resolve(config json.RawMessage) (ResolvedPorts, error)
	Execute(ctx context.Context, request Request) (Result, error)
}

type Registrar interface {
	Register(node Node) error
}

type Request struct {
	Inputs   map[string][]any `json:"inputs"`
	RunInput map[string]any   `json:"runInput"`
	Config   json.RawMessage  `json:"config"`
}

type Result struct {
	Outputs     map[string]any `json:"outputs"`
	ActivePorts []string       `json:"activePorts,omitempty"`
}
