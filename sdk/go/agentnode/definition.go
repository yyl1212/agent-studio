package agentnode

import "encoding/json"

type DataType string

const (
	DataTypeString  DataType = "string"
	DataTypeNumber  DataType = "number"
	DataTypeBoolean DataType = "boolean"
	DataTypeJSON    DataType = "json"
	DataTypeAny     DataType = "any"
)

type Cardinality string

const (
	CardinalityOne          Cardinality = "one"
	CardinalitySingleActive Cardinality = "single-active"
)

type ExecutionSafety string

const (
	ExecutionSafetyPure       ExecutionSafety = "pure"
	ExecutionSafetyReadOnly   ExecutionSafety = "read_only"
	ExecutionSafetySideEffect ExecutionSafety = "side_effect"
)

func EffectiveExecutionSafety(value ExecutionSafety) ExecutionSafety {
	switch value {
	case ExecutionSafetyPure, ExecutionSafetyReadOnly, ExecutionSafetySideEffect:
		return value
	default:
		return ExecutionSafetySideEffect
	}
}

type Port struct {
	Key         string      `json:"key"`
	Title       string      `json:"title"`
	Type        DataType    `json:"type"`
	Required    bool        `json:"required"`
	Cardinality Cardinality `json:"cardinality"`
}

type ResolvedPorts struct {
	Inputs  []Port `json:"inputs"`
	Outputs []Port `json:"outputs"`
}

type Definition struct {
	Type            string          `json:"type"`
	Version         string          `json:"version"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Category        string          `json:"category"`
	ConfigSchema    json.RawMessage `json:"configSchema"`
	Inputs          []Port          `json:"inputs"`
	Outputs         []Port          `json:"outputs"`
	Capabilities    []Capability    `json:"capabilities,omitempty"`
	ExecutionSafety ExecutionSafety `json:"executionSafety,omitempty"`
}
