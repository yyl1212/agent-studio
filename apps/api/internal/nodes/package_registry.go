package nodes

import (
	"errors"
	"fmt"
	"sort"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrPackageNodeSetMismatch = errors.New("node package registered node set does not match manifest")
	ErrPackageNodeDuplicate   = errors.New("node package node conflicts with registry")
)

type CatalogEntry struct {
	Definition agentnode.Definition
	Package    nodepackage.Summary
}

func (r *Registry) RegisterPackage(record nodepackage.RuntimeRecord, register func(agentnode.Registrar) error) error {
	expected := append([]nodepackage.NodeRef(nil), record.Nodes...)
	sort.Slice(expected, func(left, right int) bool {
		if expected[left].Type != expected[right].Type {
			return expected[left].Type < expected[right].Type
		}
		return expected[left].Version < expected[right].Version
	})
	staging := NewRegistry()
	if register == nil {
		return fmt.Errorf("register node package %s: registration callback is required", record.Summary.Name)
	}
	if err := register(staging); err != nil {
		return fmt.Errorf("register node package %s: %w", record.Summary.Name, err)
	}
	actualDefinitions := staging.Definitions()
	if len(actualDefinitions) != len(expected) {
		return fmt.Errorf("%w: %s", ErrPackageNodeSetMismatch, record.Summary.Name)
	}
	for index, definition := range actualDefinitions {
		if definition.Type != expected[index].Type || definition.Version != expected[index].Version {
			return fmt.Errorf(
				"%w: %s expected %s@%s, registered %s@%s",
				ErrPackageNodeSetMismatch, record.Summary.Name,
				expected[index].Type, expected[index].Version, definition.Type, definition.Version,
			)
		}
	}
	for key := range staging.entries {
		if _, exists := r.entries[key]; exists {
			return fmt.Errorf("%w: %s %s", ErrPackageNodeDuplicate, record.Summary.Name, key)
		}
	}
	for key, entry := range staging.entries {
		r.entries[key] = entry
		r.packageByNode[key] = record.Summary
	}
	return nil
}

func (r *Registry) PackageFor(nodeType, version string) (nodepackage.Summary, bool) {
	summary, ok := r.packageByNode[registryKey(nodeType, version)]
	return summary, ok
}

func (r *Registry) Catalog() []CatalogEntry {
	catalog := make([]CatalogEntry, 0, len(r.packageByNode))
	for key, summary := range r.packageByNode {
		entry, exists := r.entries[key]
		if !exists {
			continue
		}
		catalog = append(catalog, CatalogEntry{
			Definition: cloneDefinition(entry.definition),
			Package:    summary,
		})
	}
	sort.Slice(catalog, func(left, right int) bool {
		if catalog[left].Definition.Type != catalog[right].Definition.Type {
			return catalog[left].Definition.Type < catalog[right].Definition.Type
		}
		return catalog[left].Definition.Version < catalog[right].Definition.Version
	})
	return catalog
}
