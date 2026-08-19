package nodes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrDuplicateNodeType = errors.New("node type already registered")
	ErrNodeTypeNotFound  = errors.New("node type not found")
)

type NodeType = agentnode.Node

type registeredNode struct {
	node       NodeType
	definition agentnode.Definition
	schema     *jsonschema.Schema
}

type Registry struct {
	entries       map[string]registeredNode
	packageByNode map[string]nodepackage.Summary
}

func NewRegistry() *Registry {
	return &Registry{
		entries:       make(map[string]registeredNode),
		packageByNode: make(map[string]nodepackage.Summary),
	}
}

var _ agentnode.Registrar = (*Registry)(nil)

func (r *Registry) Register(node agentnode.Node) error {
	adapted, err := Adapt(node)
	if err != nil {
		return err
	}
	definition := adapted.Definition()
	key := registryKey(definition.Type, definition.Version)
	if _, exists := r.entries[key]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateNodeType, key)
	}

	resource := "urn:agent-studio:node:" + key
	compiled, err := compileConfigSchema(definition.ConfigSchema, resource)
	if err != nil {
		return fmt.Errorf("compile config schema for %s: %w", key, err)
	}

	r.entries[key] = registeredNode{node: adapted, definition: definition, schema: compiled}
	return nil
}

func (r *Registry) Get(nodeType, version string) (NodeType, error) {
	entry, ok := r.entries[registryKey(nodeType, version)]
	if !ok {
		return nil, fmt.Errorf("%w: %s@%s", ErrNodeTypeNotFound, nodeType, version)
	}
	return entry.node, nil
}

func (r *Registry) Definitions() []agentnode.Definition {
	definitions := make([]agentnode.Definition, 0, len(r.entries))
	for _, entry := range r.entries {
		definitions = append(definitions, cloneDefinition(entry.definition))
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
