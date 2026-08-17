package domain

import "encoding/json"

type DataType string

const (
	TypeString  DataType = "string"
	TypeNumber  DataType = "number"
	TypeBoolean DataType = "boolean"
	TypeJSON    DataType = "json"
	TypeAny     DataType = "any"
)

type PortCardinality string

const (
	CardinalityOne          PortCardinality = "one"
	CardinalitySingleActive PortCardinality = "single-active"
)

type PortDefinition struct {
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Type        DataType        `json:"type"`
	Required    bool            `json:"required"`
	Cardinality PortCardinality `json:"cardinality"`
}

type NodeRequest struct {
	Inputs   map[string][]any
	RunInput map[string]any
	Config   json.RawMessage
}

type NodeResult struct {
	Outputs     map[string]any
	ActivePorts []string
}

type ResolvedPorts struct {
	Inputs  []PortDefinition `json:"inputs"`
	Outputs []PortDefinition `json:"outputs"`
}

type NodeDefinition struct {
	Type         string           `json:"type"`
	Version      string           `json:"version"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Category     string           `json:"category"`
	ConfigSchema json.RawMessage  `json:"configSchema"`
	Inputs       []PortDefinition `json:"inputs"`
	Outputs      []PortDefinition `json:"outputs"`
}
