package domain

import "github.com/yyl1212/agent-studio/sdk/go/agentnode"

type DataType = agentnode.DataType

const (
	TypeString  = agentnode.DataTypeString
	TypeNumber  = agentnode.DataTypeNumber
	TypeBoolean = agentnode.DataTypeBoolean
	TypeJSON    = agentnode.DataTypeJSON
	TypeAny     = agentnode.DataTypeAny
)

type PortCardinality = agentnode.Cardinality

const (
	CardinalityOne          = agentnode.CardinalityOne
	CardinalitySingleActive = agentnode.CardinalitySingleActive
)

type PortDefinition = agentnode.Port
type NodeRequest = agentnode.Request
type NodeResult = agentnode.Result
type ResolvedPorts = agentnode.ResolvedPorts
type NodeDefinition = agentnode.Definition
