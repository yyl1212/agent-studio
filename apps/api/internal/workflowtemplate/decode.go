package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

func Decode(raw json.RawMessage) (Template, error) {
	var decoded Template
	if err := decodeStrictJSON(raw, &decoded); err != nil {
		return Template{}, err
	}
	missing, err := requiredV1Alpha1Fields(raw)
	if err != nil {
		return Template{}, err
	}
	decoded.missingRequired = missing
	return decoded, nil
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
