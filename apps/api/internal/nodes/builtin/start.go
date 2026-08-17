package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"

	"agentstudio.local/api/internal/domain"
)

var fieldKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type startNode struct{}

type startConfig struct {
	Fields []startField `json:"fields"`
}

type startField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"`
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
}

func NewStart() *startNode {
	return &startNode{}
}

func (*startNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:        "start",
		Version:     "1",
		Title:       "开始",
		Description: "定义 Agent 运行参数",
		Category:    "流程",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "fields":{
              "type":"array",
              "items":{
                "type":"object",
                "properties":{
                  "key":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","title":"字段标识"},
                  "label":{"type":"string","minLength":1,"title":"字段标题"},
                  "description":{"type":"string","title":"字段说明"},
                  "type":{"type":"string","enum":["text","textarea","number","boolean","select","json"],"title":"字段类型"},
                  "required":{"type":"boolean","default":false,"title":"必填"},
                  "default":{"title":"默认值"},
                  "placeholder":{"type":"string","title":"占位提示"},
                  "options":{"type":"array","items":{"type":"string"},"title":"选项"}
                },
                "required":["key","label","type"],
                "additionalProperties":false
              }
            }
          },
          "required":["fields"],
          "additionalProperties":false
        }`),
	}
}

func (*startNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
	parsed, err := parseStartConfig(config)
	if err != nil {
		return domain.ResolvedPorts{}, err
	}
	outputs := make([]domain.PortDefinition, 0, len(parsed.Fields))
	for _, field := range parsed.Fields {
		outputs = append(outputs, domain.PortDefinition{
			Key:         field.Key,
			Title:       field.Label,
			Type:        startFieldDataType(field.Type),
			Cardinality: domain.CardinalityOne,
		})
	}
	return domain.ResolvedPorts{Outputs: outputs}, nil
}

func (*startNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	parsed, err := parseStartConfig(request.Config)
	if err != nil {
		return domain.NodeResult{}, err
	}
	outputs := make(map[string]any, len(parsed.Fields))
	for _, field := range parsed.Fields {
		value, exists := request.RunInput[field.Key]
		if !exists && field.Default != nil {
			value, exists = field.Default, true
		}
		if !exists || value == nil {
			if field.Required {
				return domain.NodeResult{}, fmt.Errorf("%w: %s", ErrRequiredInputMissing, field.Key)
			}
			continue
		}
		if !validStartFieldValue(field, value) {
			return domain.NodeResult{}, fmt.Errorf("%w: %s", ErrInputTypeMismatch, field.Key)
		}
		outputs[field.Key] = value
	}
	return domain.NodeResult{Outputs: outputs}, nil
}

func DeriveInputSchema(config json.RawMessage) (json.RawMessage, error) {
	parsed, err := parseStartConfig(config)
	if err != nil {
		return nil, err
	}
	properties := make(map[string]any, len(parsed.Fields))
	required := make([]string, 0, len(parsed.Fields))
	order := make([]string, 0, len(parsed.Fields))
	for _, field := range parsed.Fields {
		property := map[string]any{"title": field.Label}
		switch field.Type {
		case "text":
			property["type"] = "string"
			property["x-ui-widget"] = "text"
		case "textarea":
			property["type"] = "string"
			property["x-ui-widget"] = "textarea"
		case "select":
			property["type"] = "string"
			property["x-ui-widget"] = "select"
			property["enum"] = field.Options
		case "number":
			property["type"] = "number"
		case "boolean":
			property["type"] = "boolean"
		case "json":
			property["x-ui-widget"] = "json"
		}
		if field.Description != "" {
			property["description"] = field.Description
		}
		if field.Default != nil {
			property["default"] = field.Default
		}
		if field.Placeholder != "" {
			property["x-ui-placeholder"] = field.Placeholder
		}
		properties[field.Key] = property
		order = append(order, field.Key)
		if field.Required {
			required = append(required, field.Key)
		}
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
		"x-ui-order":           order,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return json.Marshal(schema)
}

func parseStartConfig(raw json.RawMessage) (startConfig, error) {
	var config startConfig
	if err := decodeConfig(raw, &config); err != nil {
		return startConfig{}, err
	}
	seen := make(map[string]struct{}, len(config.Fields))
	for _, field := range config.Fields {
		if !fieldKeyPattern.MatchString(field.Key) || field.Label == "" {
			return startConfig{}, fmt.Errorf("%w: invalid field %q", ErrInvalidConfig, field.Key)
		}
		if _, exists := seen[field.Key]; exists {
			return startConfig{}, fmt.Errorf("%w: duplicate field %q", ErrInvalidConfig, field.Key)
		}
		seen[field.Key] = struct{}{}
		if startFieldDataType(field.Type) == "" {
			return startConfig{}, fmt.Errorf("%w: unsupported field type %q", ErrInvalidConfig, field.Type)
		}
		if field.Type == "select" && len(field.Options) == 0 {
			return startConfig{}, fmt.Errorf("%w: select field %q requires options", ErrInvalidConfig, field.Key)
		}
		if field.Default != nil && !validStartFieldValue(field, field.Default) {
			return startConfig{}, fmt.Errorf("%w: invalid default for %q", ErrInvalidConfig, field.Key)
		}
	}
	return config, nil
}

func startFieldDataType(fieldType string) domain.DataType {
	switch fieldType {
	case "text", "textarea", "select":
		return domain.TypeString
	case "number":
		return domain.TypeNumber
	case "boolean":
		return domain.TypeBoolean
	case "json":
		return domain.TypeJSON
	default:
		return ""
	}
}

func validStartFieldValue(field startField, value any) bool {
	switch field.Type {
	case "text", "textarea":
		_, ok := value.(string)
		return ok
	case "select":
		text, ok := value.(string)
		if !ok {
			return false
		}
		for _, option := range field.Options {
			if text == option {
				return true
			}
		}
		return false
	case "number":
		kind := reflect.TypeOf(value).Kind()
		return kind >= reflect.Int && kind <= reflect.Float64
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "json":
		_, err := json.Marshal(value)
		return err == nil
	default:
		return false
	}
}
