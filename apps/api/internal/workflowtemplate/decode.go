package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
)

func Decode(raw json.RawMessage) (Template, error) {
	if !utf8.Valid(raw) {
		return Template{}, fmt.Errorf("decode workflow template: invalid UTF-8")
	}
	var probe struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return Template{}, fmt.Errorf("decode workflow template version: %w", err)
	}

	var decoded Template
	if probe.APIVersion == APIVersionV1Alpha1 {
		var legacy templateV1Alpha1
		if err := decodeStrictJSON(raw, &legacy); err != nil {
			return Template{}, err
		}
		decoded = Template{
			APIVersion: APIVersionV1Alpha2,
			Kind:       legacy.Kind,
			Metadata:   legacy.Metadata,
			Spec:       Spec{NodePackages: []NodePackageRequirement{}, Graph: legacy.Spec.Graph},
		}
	} else {
		if err := decodeStrictJSON(raw, &decoded); err != nil {
			return Template{}, err
		}
		if hasNullNodePackages(raw) {
			return Template{}, fmt.Errorf("decode workflow template: spec.nodePackages must be an array when present")
		}
		if decoded.Spec.NodePackages == nil {
			decoded.Spec.NodePackages = []NodePackageRequirement{}
		}
		if err := validatePackageRequirements(decoded.Spec.NodePackages); err != nil {
			return Template{}, err
		}
	}
	missing, err := requiredV1Alpha1Fields(raw)
	if err != nil {
		return Template{}, err
	}
	decoded.missingRequired = missing
	return decoded, nil
}

func hasNullNodePackages(raw json.RawMessage) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	var spec map[string]json.RawMessage
	if json.Unmarshal(root["spec"], &spec) != nil {
		return false
	}
	nodePackages, present := spec["nodePackages"]
	return present && bytes.Equal(bytes.TrimSpace(nodePackages), []byte("null"))
}

type templateV1Alpha1 struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   Metadata     `json:"metadata"`
	Spec       specV1Alpha1 `json:"spec"`
}

type specV1Alpha1 struct {
	Graph domain.Graph `json:"graph"`
}

func validatePackageRequirements(requirements []NodePackageRequirement) error {
	if len(requirements) > MaxNodePackages {
		return fmt.Errorf("decode workflow template: nodePackages exceeds %d packages", MaxNodePackages)
	}
	packageNames := make(map[string]struct{}, len(requirements))
	nodeKeys := make(map[string]struct{})
	totalNodes := 0
	for packageIndex, requirement := range requirements {
		path := fmt.Sprintf("spec.nodePackages[%d]", packageIndex)
		if !utf8.ValidString(requirement.Name) || utf8.RuneCountInString(requirement.Name) > nodepackage.MaxModulePathLength ||
			strings.TrimSpace(requirement.Name) != requirement.Name || module.CheckPath(requirement.Name) != nil {
			return fmt.Errorf("decode workflow template: %s.name is invalid", path)
		}
		if _, duplicate := packageNames[requirement.Name]; duplicate {
			return fmt.Errorf("decode workflow template: %s.name is duplicated", path)
		}
		packageNames[requirement.Name] = struct{}{}
		if requirement.Version != "" && (!utf8.ValidString(requirement.Version) ||
			utf8.RuneCountInString(requirement.Version) > nodepackage.MaxVersionLength || !semver.IsValid(requirement.Version)) {
			return fmt.Errorf("decode workflow template: %s.version is invalid", path)
		}
		if len(requirement.Nodes) == 0 {
			return fmt.Errorf("decode workflow template: %s.nodes must not be empty", path)
		}
		totalNodes += len(requirement.Nodes)
		if totalNodes > MaxPackageNodes {
			return fmt.Errorf("decode workflow template: nodePackages exceeds %d node references", MaxPackageNodes)
		}
		for nodeIndex, node := range requirement.Nodes {
			nodePath := fmt.Sprintf("%s.nodes[%d]", path, nodeIndex)
			if !validHintString(node.Type, nodepackage.MaxNodeTypeLength) {
				return fmt.Errorf("decode workflow template: %s.type is invalid", nodePath)
			}
			if !validHintString(node.Version, nodepackage.MaxVersionLength) {
				return fmt.Errorf("decode workflow template: %s.version is invalid", nodePath)
			}
			key := node.Type + "@" + node.Version
			if _, duplicate := nodeKeys[key]; duplicate {
				return fmt.Errorf("decode workflow template: %s is duplicated", nodePath)
			}
			nodeKeys[key] = struct{}{}
		}
	}
	return nil
}

func validHintString(value string, maximum int) bool {
	return value != "" && utf8.ValidString(value) && strings.TrimSpace(value) == value && utf8.RuneCountInString(value) <= maximum
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode workflow template: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode trailing workflow template JSON: %w", err)
	}
	return nil
}

func requiredV1Alpha1Fields(raw json.RawMessage) ([]string, error) {
	root, err := decodeRequiredObject(raw, "template")
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0)
	requireField(root, "apiVersion", "apiVersion", &missing)
	requireField(root, "kind", "kind", &missing)

	metadataRaw, hasMetadata := requireField(root, "metadata", "metadata", &missing)
	if hasMetadata {
		metadata, decodeErr := decodeRequiredObject(metadataRaw, "metadata")
		if decodeErr != nil {
			return nil, decodeErr
		}
		requireField(metadata, "name", "metadata.name", &missing)
		requireField(metadata, "description", "metadata.description", &missing)
	}

	specRaw, hasSpec := requireField(root, "spec", "spec", &missing)
	if hasSpec {
		spec, decodeErr := decodeRequiredObject(specRaw, "spec")
		if decodeErr != nil {
			return nil, decodeErr
		}
		graphRaw, hasGraph := requireField(spec, "graph", "spec.graph", &missing)
		if hasGraph {
			if decodeErr := requiredGraphFields(graphRaw, &missing); decodeErr != nil {
				return nil, decodeErr
			}
		}
	}

	sort.Strings(missing)
	return missing, nil
}

func requiredGraphFields(raw json.RawMessage, missing *[]string) error {
	graph, err := decodeRequiredObject(raw, "spec.graph")
	if err != nil {
		return err
	}
	requireField(graph, "schemaVersion", "spec.graph.schemaVersion", missing)

	nodesRaw, hasNodes := requireField(graph, "nodes", "spec.graph.nodes", missing)
	if hasNodes {
		nodes, decodeErr := decodeRequiredArray(nodesRaw, "spec.graph.nodes")
		if decodeErr != nil {
			return decodeErr
		}
		for index, nodeRaw := range nodes {
			if decodeErr := requiredNodeFields(nodeRaw, index, missing); decodeErr != nil {
				return decodeErr
			}
		}
	}

	edgesRaw, hasEdges := requireField(graph, "edges", "spec.graph.edges", missing)
	if hasEdges {
		edges, decodeErr := decodeRequiredArray(edgesRaw, "spec.graph.edges")
		if decodeErr != nil {
			return decodeErr
		}
		for index, edgeRaw := range edges {
			if decodeErr := requiredEdgeFields(edgeRaw, index, missing); decodeErr != nil {
				return decodeErr
			}
		}
	}
	return nil
}

func requiredNodeFields(raw json.RawMessage, index int, missing *[]string) error {
	parent := fmt.Sprintf("spec.graph.nodes[%d]", index)
	node, err := decodeRequiredObject(raw, parent)
	if err != nil {
		return err
	}
	for _, field := range []string{"id", "type", "typeVersion", "position", "config"} {
		requireField(node, field, parent+"."+field, missing)
	}
	positionRaw, hasPosition := node["position"]
	if !hasPosition {
		return nil
	}
	position, err := decodeRequiredObject(positionRaw, parent+".position")
	if err != nil {
		return err
	}
	requireField(position, "x", parent+".position.x", missing)
	requireField(position, "y", parent+".position.y", missing)
	return nil
}

func requiredEdgeFields(raw json.RawMessage, index int, missing *[]string) error {
	parent := fmt.Sprintf("spec.graph.edges[%d]", index)
	edge, err := decodeRequiredObject(raw, parent)
	if err != nil {
		return err
	}
	for _, field := range []string{"id", "source", "sourcePort", "target", "targetPort"} {
		requireField(edge, field, parent+"."+field, missing)
	}
	return nil
}

func requireField(object map[string]json.RawMessage, field, path string, missing *[]string) (json.RawMessage, bool) {
	raw, exists := object[field]
	if !exists {
		*missing = append(*missing, path)
	}
	return raw, exists
}

func decodeRequiredObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("decode required fields at %s: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("decode required fields at %s: expected object", path)
	}
	return object, nil
}

func decodeRequiredArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode required fields at %s: %w", path, err)
	}
	if values == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		values = []json.RawMessage{}
	}
	return values, nil
}
