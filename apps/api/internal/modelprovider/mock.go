package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

type Mock struct{}

func NewMock() *Mock {
	return &Mock{}
}

func (*Mock) Complete(_ context.Context, request Request) (Response, error) {
	text := "Mock 回复：" + request.Prompt
	if request.ResponseFormat != nil {
		encoded, err := mockStructuredOutput(request.Prompt, request.ResponseFormat.Schema)
		if err != nil {
			return Response{}, err
		}
		text = string(encoded)
	}
	return Response{
		Text: text,
		Usage: map[string]int{
			"promptTokens":     0,
			"completionTokens": 0,
			"totalTokens":      0,
		},
	}, nil
}

type mockStructuredSchema struct {
	Type                 string                         `json:"type"`
	Properties           map[string]mockStructuredField `json:"properties"`
	Required             []string                       `json:"required"`
	AdditionalProperties *bool                          `json:"additionalProperties"`
}

type mockStructuredField struct {
	Type        json.RawMessage      `json:"type"`
	Description string               `json:"description,omitempty"`
	Items       *mockStructuredItems `json:"items,omitempty"`
}

type mockStructuredItems struct {
	Type string `json:"type"`
}

func mockStructuredOutput(prompt string, schemaJSON json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(schemaJSON))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var schema mockStructuredSchema
	if err := decoder.Decode(&schema); err != nil {
		return nil, invalidMockSchema()
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, invalidMockSchema()
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties || len(schema.Properties) == 0 {
		return nil, invalidMockSchema()
	}
	required := make(map[string]struct{}, len(schema.Required))
	for _, key := range schema.Required {
		if _, exists := schema.Properties[key]; !exists {
			return nil, invalidMockSchema()
		}
		if _, duplicate := required[key]; duplicate {
			return nil, invalidMockSchema()
		}
		required[key] = struct{}{}
	}
	if len(required) != len(schema.Properties) {
		return nil, invalidMockSchema()
	}

	result := make(map[string]any, len(schema.Properties))
	for key, property := range schema.Properties {
		baseType, nullable, ok := mockFieldType(property.Type)
		if !ok || (baseType == "array") != (property.Items != nil) {
			return nil, invalidMockSchema()
		}
		if property.Items != nil && property.Items.Type != "string" {
			return nil, invalidMockSchema()
		}
		if nullable {
			result[key] = nil
			continue
		}
		switch baseType {
		case "string":
			result[key] = "Mock 回复：" + prompt
		case "number", "integer":
			result[key] = json.Number("0")
		case "boolean":
			result[key] = false
		case "array":
			result[key] = []any{}
		default:
			return nil, invalidMockSchema()
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("%w: encode structured mock output", ErrInvalidResponse)
	}
	return encoded, nil
}

func mockFieldType(raw json.RawMessage) (string, bool, bool) {
	var scalar string
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return scalar, false, supportedMockFieldType(scalar)
	}
	var types []string
	if err := json.Unmarshal(raw, &types); err != nil || len(types) != 2 {
		return "", false, false
	}
	if types[0] == "null" {
		types[0], types[1] = types[1], types[0]
	}
	return types[0], types[1] == "null", types[1] == "null" && supportedMockFieldType(types[0])
}

func supportedMockFieldType(value string) bool {
	switch value {
	case "string", "number", "integer", "boolean", "array":
		return true
	default:
		return false
	}
}

func invalidMockSchema() error {
	return fmt.Errorf("%w: unsupported structured schema", ErrInvalidResponse)
}
