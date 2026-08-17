package nodemanifest

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/module"
)

type Manifest struct {
	APIVersion string        `yaml:"apiVersion"`
	Nodes      []NodePackage `yaml:"nodes"`
}

type NodePackage struct {
	Package string `yaml:"package"`
}

func Parse(source string, data []byte) (Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse node manifest %s: %w", source, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("parse node manifest %s: multiple YAML documents are not allowed", source)
		}
		return Manifest{}, fmt.Errorf("parse node manifest %s: trailing document: %w", source, err)
	}
	if manifest.Nodes == nil {
		manifest.Nodes = []NodePackage{}
	}
	if err := validate(manifest); err != nil {
		return Manifest{}, fmt.Errorf("validate node manifest %s: %w", source, err)
	}
	return manifest, nil
}

func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read node manifest %s: %w", path, err)
	}
	return Parse(path, data)
}

func Marshal(manifest Manifest) ([]byte, error) {
	if manifest.Nodes == nil {
		manifest.Nodes = []NodePackage{}
	}
	if err := validate(manifest); err != nil {
		return nil, fmt.Errorf("validate node manifest: %w", err)
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("encode node manifest: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close node manifest encoder: %w", err)
	}
	return output.Bytes(), nil
}

func AddPackage(manifest Manifest, importPath string) (Manifest, error) {
	updated := Manifest{
		APIVersion: manifest.APIVersion,
		Nodes:      append([]NodePackage(nil), manifest.Nodes...),
	}
	updated.Nodes = append(updated.Nodes, NodePackage{Package: importPath})
	if err := validate(updated); err != nil {
		return Manifest{}, err
	}
	return updated, nil
}

func validate(manifest Manifest) error {
	if manifest.APIVersion != agentnode.APIVersion {
		return fmt.Errorf("unsupported apiVersion %q; want %q", manifest.APIVersion, agentnode.APIVersion)
	}
	seen := make(map[string]struct{}, len(manifest.Nodes))
	for index, node := range manifest.Nodes {
		if node.Package == "" {
			return fmt.Errorf("nodes[%d].package is required", index)
		}
		if err := module.CheckImportPath(node.Package); err != nil {
			return fmt.Errorf("nodes[%d] has invalid package %q: %w", index, node.Package, err)
		}
		if _, exists := seen[node.Package]; exists {
			return fmt.Errorf("duplicate package %q", node.Package)
		}
		seen[node.Package] = struct{}{}
	}
	return nil
}
