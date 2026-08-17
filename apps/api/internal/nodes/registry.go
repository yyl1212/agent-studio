package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"agentstudio.local/api/internal/domain"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrDuplicateNodeType = errors.New("node type already registered")
	ErrNodeTypeNotFound  = errors.New("node type not found")
)

type NodeType interface {
	Definition() domain.NodeDefinition
	Resolve(config json.RawMessage) (domain.ResolvedPorts, error)
	Execute(ctx context.Context, request domain.NodeRequest) (domain.NodeResult, error)
}

type registeredNode struct {
	node   NodeType
	schema *jsonschema.Schema
}

type Registry struct {
	entries map[string]registeredNode
}

func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]registeredNode)}
}

func (r *Registry) Register(node NodeType) error {
	definition := node.Definition()
	key := registryKey(definition.Type, definition.Version)
	if _, exists := r.entries[key]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateNodeType, key)
	}

	resource := "urn:agent-studio:node:" + key
	compiled, err := compileConfigSchema(definition.ConfigSchema, resource)
	if err != nil {
		return fmt.Errorf("compile config schema for %s: %w", key, err)
	}

	r.entries[key] = registeredNode{node: node, schema: compiled}
	return nil
}

func (r *Registry) Get(nodeType, version string) (NodeType, error) {
	entry, ok := r.entries[registryKey(nodeType, version)]
	if !ok {
		return nil, fmt.Errorf("%w: %s@%s", ErrNodeTypeNotFound, nodeType, version)
	}
	return entry.node, nil
}

func (r *Registry) Definitions() []domain.NodeDefinition {
	definitions := make([]domain.NodeDefinition, 0, len(r.entries))
	for _, entry := range r.entries {
		definition := entry.node.Definition()
		if definition.Inputs == nil {
			definition.Inputs = []domain.PortDefinition{}
		}
		if definition.Outputs == nil {
			definition.Outputs = []domain.PortDefinition{}
		}
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Type == definitions[j].Type {
			return definitions[i].Version < definitions[j].Version
		}
		return definitions[i].Type < definitions[j].Type
	})
	return definitions
}

func (r *Registry) ValidateConfig(nodeType, version string, config json.RawMessage) error {
	entry, ok := r.entries[registryKey(nodeType, version)]
	if !ok {
		return fmt.Errorf("%w: %s@%s", ErrNodeTypeNotFound, nodeType, version)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(config))
	if err != nil {
		return fmt.Errorf("parse config for %s@%s: %w", nodeType, version, err)
	}
	if err := entry.schema.Validate(value); err != nil {
		return fmt.Errorf("validate config for %s@%s: %w", nodeType, version, err)
	}
	return nil
}

func registryKey(nodeType, version string) string {
	return nodeType + "@" + version
}
