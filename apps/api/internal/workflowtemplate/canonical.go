package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func Canonicalize(input Template) (Template, error) {
	output := input
	output.Spec.Graph.Nodes = append([]domain.Node(nil), input.Spec.Graph.Nodes...)
	output.Spec.Graph.Edges = append([]domain.Edge(nil), input.Spec.Graph.Edges...)
	if output.Spec.Graph.Nodes == nil {
		output.Spec.Graph.Nodes = []domain.Node{}
	}
	if output.Spec.Graph.Edges == nil {
		output.Spec.Graph.Edges = []domain.Edge{}
	}
	for index := range output.Spec.Graph.Nodes {
		value, err := decodeJSONValue(output.Spec.Graph.Nodes[index].Config)
		if err != nil {
			return Template{}, fmt.Errorf("canonicalize config for node %s: %w", output.Spec.Graph.Nodes[index].ID, err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return Template{}, fmt.Errorf("encode config for node %s: %w", output.Spec.Graph.Nodes[index].ID, err)
		}
		output.Spec.Graph.Nodes[index].Config = encoded
	}
	sort.Slice(output.Spec.Graph.Nodes, func(i, j int) bool {
		return output.Spec.Graph.Nodes[i].ID < output.Spec.Graph.Nodes[j].ID
	})
	sort.Slice(output.Spec.Graph.Edges, func(i, j int) bool {
		return output.Spec.Graph.Edges[i].ID < output.Spec.Graph.Edges[j].ID
	})
	return output, nil
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func Encode(input Template) ([]byte, error) {
	normalized, err := Canonicalize(input)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workflow template: %w", err)
	}
	return append(encoded, '\n'), nil
}
