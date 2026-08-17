package agenttest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	typePattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9-]*)*$`)
	versionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)
)

type Contract struct {
	Node           agentnode.Node
	ValidConfigs   []json.RawMessage
	InvalidConfigs []json.RawMessage
	Executions     []ExecutionCase
	Cancellation   *CancellationCase
	MaxOutputBytes int
}

func Run(t *testing.T, contract Contract) {
	t.Helper()
	if contract.Node == nil {
		t.Error("agenttest: node is required")
		return
	}
	if err := validateDefinition(contract.Node.Definition()); err != nil {
		t.Errorf("agenttest: definition: %v", err)
		return
	}
	if err := validateConfigCases(contract.Node, contract.ValidConfigs, contract.InvalidConfigs); err != nil {
		t.Errorf("agenttest: config: %v", err)
	}
	for _, execution := range contract.Executions {
		if err := validateExecutionCase(contract.Node, execution, contract.MaxOutputBytes); err != nil {
			t.Errorf("agenttest: execution %q: %v", execution.Name, err)
		}
	}
	if contract.Cancellation != nil {
		if err := validateCancellation(contract.Node, *contract.Cancellation); err != nil {
			t.Errorf("agenttest: cancellation: %v", err)
		}
	}
}

func validateDefinition(definition agentnode.Definition) error {
	if !typePattern.MatchString(definition.Type) {
		return fmt.Errorf("type %q does not match %s", definition.Type, typePattern)
	}
	if !versionPattern.MatchString(definition.Version) {
		return fmt.Errorf("version %q does not match %s", definition.Version, versionPattern)
	}
	if err := validatePorts("input", definition.Inputs); err != nil {
		return err
	}
	if err := validatePorts("output", definition.Outputs); err != nil {
		return err
	}
	seenCapabilities := make(map[agentnode.Capability]struct{}, len(definition.Capabilities))
	for _, capability := range definition.Capabilities {
		if !validCapability(capability) {
			return fmt.Errorf("unknown capability %q", capability)
		}
		if _, exists := seenCapabilities[capability]; exists {
			return fmt.Errorf("duplicate capability %q", capability)
		}
		seenCapabilities[capability] = struct{}{}
	}
	if err := compileSchema(definition.ConfigSchema); err != nil {
		return err
	}
	return nil
}

func validateConfigCases(node agentnode.Node, validConfigs, invalidConfigs []json.RawMessage) error {
	for index, config := range validConfigs {
		ports, err := node.Resolve(config)
		if err != nil {
			return fmt.Errorf("valid config %d was rejected: %w", index, err)
		}
		if err := validatePorts("resolved input", ports.Inputs); err != nil {
			return fmt.Errorf("valid config %d: %w", index, err)
		}
		if err := validatePorts("resolved output", ports.Outputs); err != nil {
			return fmt.Errorf("valid config %d: %w", index, err)
		}
	}
	for index, config := range invalidConfigs {
		_, err := node.Resolve(config)
		if err == nil {
			return fmt.Errorf("invalid config %d was accepted", index)
		}
		if kind := agentnode.KindOf(err); kind != agentnode.ErrorKindConfig {
			return fmt.Errorf("invalid config %d returned error kind %q, want %q: %w", index, kind, agentnode.ErrorKindConfig, err)
		}
	}
	return nil
}

func validatePorts(kind string, ports []agentnode.Port) error {
	seen := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		if port.Key == "" {
			return fmt.Errorf("%s port key is empty", kind)
		}
		if _, exists := seen[port.Key]; exists {
			return fmt.Errorf("duplicate %s port %q", kind, port.Key)
		}
		if !validDataType(port.Type) {
			return fmt.Errorf("%s port %q has unknown data type %q", kind, port.Key, port.Type)
		}
		if !validCardinality(port.Cardinality) {
			return fmt.Errorf("%s port %q has unknown cardinality %q", kind, port.Key, port.Cardinality)
		}
		seen[port.Key] = struct{}{}
	}
	return nil
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

func compileSchema(raw json.RawMessage) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse config schema: %w", err)
	}
	const resource = "urn:agent-studio:agenttest:schema"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resource, document); err != nil {
		return fmt.Errorf("add config schema: %w", err)
	}
	if _, err := compiler.Compile(resource); err != nil {
		return fmt.Errorf("compile config schema: %w", err)
	}
	return nil
}
