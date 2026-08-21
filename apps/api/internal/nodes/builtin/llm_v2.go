package builtin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	maxLLMV2Fields           = 32
	maxLLMV2FieldLabelRunes  = 80
	maxLLMV2DescriptionRunes = 500
)

var llmV2FieldKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type llmV2FieldType string

const (
	llmV2FieldString      llmV2FieldType = "string"
	llmV2FieldNumber      llmV2FieldType = "number"
	llmV2FieldInteger     llmV2FieldType = "integer"
	llmV2FieldBoolean     llmV2FieldType = "boolean"
	llmV2FieldStringArray llmV2FieldType = "string_array"
)

type llmV2Field struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Type        llmV2FieldType `json:"type"`
	Required    *bool          `json:"required,omitempty"`
}

type llmV2Config struct {
	Model        string       `json:"model,omitempty"`
	SystemPrompt string       `json:"systemPrompt,omitempty"`
	Temperature  *float64     `json:"temperature,omitempty"`
	MaxTokens    *int         `json:"maxTokens,omitempty"`
	OutputMode   string       `json:"outputMode"`
	Fields       []llmV2Field `json:"fields,omitempty"`
}

type llmV2Schema struct {
	Type                 string                `json:"type"`
	Properties           llmV2SchemaProperties `json:"properties"`
	Required             []string              `json:"required"`
	AdditionalProperties bool                  `json:"additionalProperties"`
}

type llmV2SchemaProperties []llmV2SchemaProperty

type llmV2SchemaProperty struct {
	Key    string
	Schema llmV2PropertySchema
}

type llmV2PropertySchema struct {
	Type        any                      `json:"type"`
	Description string                   `json:"description,omitempty"`
	Items       *llmV2PropertyItemSchema `json:"items,omitempty"`
}

type llmV2PropertyItemSchema struct {
	Type string `json:"type"`
}

func (properties llmV2SchemaProperties) MarshalJSON() ([]byte, error) {
	var encoded bytes.Buffer
	encoded.WriteByte('{')
	for index, property := range properties {
		if index > 0 {
			encoded.WriteByte(',')
		}
		keyJSON, err := json.Marshal(property.Key)
		if err != nil {
			return nil, err
		}
		valueJSON, err := json.Marshal(property.Schema)
		if err != nil {
			return nil, err
		}
		encoded.Write(keyJSON)
		encoded.WriteByte(':')
		encoded.Write(valueJSON)
	}
	encoded.WriteByte('}')
	return encoded.Bytes(), nil
}

func buildLLMV2Schema(fields []llmV2Field) (json.RawMessage, string, error) {
	if len(fields) == 0 || len(fields) > maxLLMV2Fields {
		return nil, "", invalidLLMV2Fields()
	}
	properties := make(llmV2SchemaProperties, 0, len(fields))
	required := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validLLMV2Field(field) {
			return nil, "", invalidLLMV2Fields()
		}
		if _, duplicate := seen[field.Key]; duplicate {
			return nil, "", invalidLLMV2Fields()
		}
		seen[field.Key] = struct{}{}
		propertyType := any(string(field.Type))
		if !llmV2FieldRequired(field) {
			propertyType = []string{string(field.Type), "null"}
		}
		property := llmV2PropertySchema{Type: propertyType, Description: field.Description}
		if field.Type == llmV2FieldStringArray {
			baseType := "array"
			if llmV2FieldRequired(field) {
				property.Type = baseType
			} else {
				property.Type = []string{baseType, "null"}
			}
			property.Items = &llmV2PropertyItemSchema{Type: "string"}
		}
		properties = append(properties, llmV2SchemaProperty{Key: field.Key, Schema: property})
		required = append(required, field.Key)
	}
	encoded, err := json.Marshal(llmV2Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	})
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode output schema", ErrInvalidConfig)
	}
	digest := sha256.Sum256(encoded)
	return json.RawMessage(encoded), fmt.Sprintf("agent_studio_%x", digest[:8]), nil
}

func validLLMV2Field(field llmV2Field) bool {
	if !llmV2FieldKeyPattern.MatchString(field.Key) || field.Key == "text" || field.Key == "json" || field.Key == "usage" {
		return false
	}
	labelRunes := utf8.RuneCountInString(field.Label)
	if labelRunes == 0 || labelRunes > maxLLMV2FieldLabelRunes || utf8.RuneCountInString(field.Description) > maxLLMV2DescriptionRunes {
		return false
	}
	switch field.Type {
	case llmV2FieldString, llmV2FieldNumber, llmV2FieldInteger, llmV2FieldBoolean, llmV2FieldStringArray:
		return true
	default:
		return false
	}
}

func llmV2OutputPorts(config llmV2Config) []agentnode.Port {
	ports := make([]agentnode.Port, 0, len(config.Fields)+2)
	ports = append(ports, agentnode.Port{Key: "json", Title: "结构化结果", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne})
	for _, field := range config.Fields {
		dataType := agentnode.DataTypeAny
		if llmV2FieldRequired(field) {
			switch field.Type {
			case llmV2FieldString:
				dataType = agentnode.DataTypeString
			case llmV2FieldNumber, llmV2FieldInteger:
				dataType = agentnode.DataTypeNumber
			case llmV2FieldBoolean:
				dataType = agentnode.DataTypeBoolean
			case llmV2FieldStringArray:
				dataType = agentnode.DataTypeJSON
			}
		}
		ports = append(ports, agentnode.Port{Key: field.Key, Title: field.Label, Type: dataType, Cardinality: agentnode.CardinalityOne})
	}
	return append(ports, agentnode.Port{Key: "usage", Title: "用量", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne})
}

func llmV2FieldRequired(field llmV2Field) bool {
	return field.Required == nil || *field.Required
}

func invalidLLMV2Fields() error {
	return fmt.Errorf("%w: invalid llm v2 fields", ErrInvalidConfig)
}
