package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrInvalidDefinition    = errors.New("invalid node definition")
	ErrInvalidResolvedPorts = errors.New("invalid resolved ports")
	ErrInvalidResult        = errors.New("invalid node result")
)

type adaptedNode struct {
	delegate   agentnode.Node
	definition agentnode.Definition
}

func Adapt(node agentnode.Node) (agentnode.Node, error) {
	if node == nil {
		return nil, fmt.Errorf("%w: node is nil", ErrInvalidDefinition)
	}
	definition, err := NormalizeDefinition(node.Definition())
	if err != nil {
		return nil, err
	}
	return &adaptedNode{delegate: node, definition: definition}, nil
}

func (node *adaptedNode) Definition() agentnode.Definition {
	return cloneDefinition(node.definition)
}

func (node *adaptedNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	ports, err := node.delegate.Resolve(config)
	if err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	return NormalizeResolvedPorts(ports)
}

func (node *adaptedNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	return node.delegate.Execute(ctx, request)
}

func NormalizeDefinition(definition agentnode.Definition) (agentnode.Definition, error) {
	if strings.TrimSpace(definition.Type) == "" {
		return agentnode.Definition{}, fmt.Errorf("%w: type is required", ErrInvalidDefinition)
	}
	if strings.TrimSpace(definition.Version) == "" {
		return agentnode.Definition{}, fmt.Errorf("%w: version is required", ErrInvalidDefinition)
	}

	inputs, err := normalizePorts(definition.Inputs, ErrInvalidDefinition, "input")
	if err != nil {
		return agentnode.Definition{}, err
	}
	outputs, err := normalizePorts(definition.Outputs, ErrInvalidDefinition, "output")
	if err != nil {
		return agentnode.Definition{}, err
	}
	seenCapabilities := make(map[agentnode.Capability]struct{}, len(definition.Capabilities))
	for _, capability := range definition.Capabilities {
		if !validCapability(capability) {
			return agentnode.Definition{}, fmt.Errorf("%w: unknown capability %q", ErrInvalidDefinition, capability)
		}
		if _, exists := seenCapabilities[capability]; exists {
			return agentnode.Definition{}, fmt.Errorf("%w: duplicate capability %q", ErrInvalidDefinition, capability)
		}
		seenCapabilities[capability] = struct{}{}
	}
	definition.Inputs = inputs
	definition.Outputs = outputs
	definition.ConfigSchema = append(json.RawMessage(nil), definition.ConfigSchema...)
	definition.Capabilities = append([]agentnode.Capability(nil), definition.Capabilities...)
	return definition, nil
}

func NormalizeResolvedPorts(ports agentnode.ResolvedPorts) (agentnode.ResolvedPorts, error) {
	inputs, err := normalizePorts(ports.Inputs, ErrInvalidResolvedPorts, "input")
	if err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	outputs, err := normalizePorts(ports.Outputs, ErrInvalidResolvedPorts, "output")
	if err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	return agentnode.ResolvedPorts{Inputs: inputs, Outputs: outputs}, nil
}

func NormalizeResult(result agentnode.Result, ports agentnode.ResolvedPorts) (agentnode.Result, error) {
	declared := make(map[string]struct{}, len(ports.Outputs))
	for _, port := range ports.Outputs {
		declared[port.Key] = struct{}{}
	}

	outputs := make(map[string]any, len(result.Outputs))
	for key, value := range result.Outputs {
		if _, ok := declared[key]; !ok {
			return agentnode.Result{}, fmt.Errorf("%w: output %q is not declared", ErrInvalidResult, key)
		}
		outputs[key] = value
	}

	activePorts := make([]string, 0, len(result.ActivePorts))
	seen := make(map[string]struct{}, len(result.ActivePorts))
	for _, key := range result.ActivePorts {
		if _, ok := declared[key]; !ok {
			return agentnode.Result{}, fmt.Errorf("%w: active port %q is not declared", ErrInvalidResult, key)
		}
		if _, ok := seen[key]; ok {
			return agentnode.Result{}, fmt.Errorf("%w: active port %q is duplicated", ErrInvalidResult, key)
		}
		seen[key] = struct{}{}
		activePorts = append(activePorts, key)
	}
	return agentnode.Result{Outputs: outputs, ActivePorts: activePorts}, nil
}

func normalizePorts(ports []agentnode.Port, sentinel error, kind string) ([]agentnode.Port, error) {
	normalized := make([]agentnode.Port, 0, len(ports))
	seen := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		if strings.TrimSpace(port.Key) == "" {
			return nil, fmt.Errorf("%w: %s port key is required", sentinel, kind)
		}
		if _, ok := seen[port.Key]; ok {
			return nil, fmt.Errorf("%w: duplicate %s port %q", sentinel, kind, port.Key)
		}
		if !validDataType(port.Type) {
			return nil, fmt.Errorf("%w: %s port %q has unknown data type %q", sentinel, kind, port.Key, port.Type)
		}
		if !validCardinality(port.Cardinality) {
			return nil, fmt.Errorf("%w: %s port %q has unknown cardinality %q", sentinel, kind, port.Key, port.Cardinality)
		}
		seen[port.Key] = struct{}{}
		normalized = append(normalized, port)
	}
	return normalized, nil
}

func validDataType(value agentnode.DataType) bool {
	switch value {
	case agentnode.DataTypeString, agentnode.DataTypeNumber, agentnode.DataTypeBoolean, agentnode.DataTypeJSON, agentnode.DataTypeAny:
		return true
	default:
		return false
	}
}

func validCardinality(value agentnode.Cardinality) bool {
	switch value {
	case agentnode.CardinalityOne, agentnode.CardinalitySingleActive:
		return true
	default:
		return false
	}
}

func validCapability(value agentnode.Capability) bool {
	switch value {
	case agentnode.CapabilityNetwork, agentnode.CapabilitySecrets, agentnode.CapabilityFilesystemRead, agentnode.CapabilityFilesystemWrite:
		return true
	default:
		return false
	}
}

func cloneDefinition(definition agentnode.Definition) agentnode.Definition {
	definition.ConfigSchema = append(json.RawMessage(nil), definition.ConfigSchema...)
	definition.Inputs = clonePorts(definition.Inputs)
	definition.Outputs = clonePorts(definition.Outputs)
	definition.Capabilities = append([]agentnode.Capability(nil), definition.Capabilities...)
	return definition
}

func clonePorts(ports []agentnode.Port) []agentnode.Port {
	if ports == nil {
		return nil
	}
	cloned := make([]agentnode.Port, len(ports))
	copy(cloned, ports)
	return cloned
}
