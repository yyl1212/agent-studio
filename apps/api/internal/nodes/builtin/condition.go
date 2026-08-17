package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"agentstudio.local/api/internal/domain"
)

type conditionNode struct{}

type conditionConfig struct {
	Operator     string `json:"operator"`
	CompareValue any    `json:"compareValue,omitempty"`
}

func NewCondition() *conditionNode {
	return &conditionNode{}
}

func (*conditionNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:        "condition",
		Version:     "1",
		Title:       "条件",
		Description: "根据输入值激活一个分支",
		Category:    "流程",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "operator":{"type":"string","enum":["equals","notEquals","contains","greaterThan","lessThan","isEmpty"],"title":"运算符"},
            "compareValue":{"title":"比较值"}
          },
          "required":["operator"],
          "additionalProperties":false
        }`),
		Inputs: []domain.PortDefinition{{
			Key:         "value",
			Title:       "值",
			Type:        domain.TypeAny,
			Required:    true,
			Cardinality: domain.CardinalityOne,
		}},
		Outputs: []domain.PortDefinition{
			{Key: "true", Title: "是", Type: domain.TypeAny, Cardinality: domain.CardinalityOne},
			{Key: "false", Title: "否", Type: domain.TypeAny, Cardinality: domain.CardinalityOne},
		},
	}
}

func (n *conditionNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
	if _, err := parseConditionConfig(config); err != nil {
		return domain.ResolvedPorts{}, err
	}
	definition := n.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (*conditionNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	config, err := parseConditionConfig(request.Config)
	if err != nil {
		return domain.NodeResult{}, err
	}
	value, err := exactlyOneInput(request.Inputs, "value")
	if err != nil {
		return domain.NodeResult{}, err
	}
	matched, err := evaluateCondition(config.Operator, value, config.CompareValue)
	if err != nil {
		return domain.NodeResult{}, err
	}
	port := "false"
	if matched {
		port = "true"
	}
	return domain.NodeResult{
		Outputs:     map[string]any{port: value},
		ActivePorts: []string{port},
	}, nil
}

func parseConditionConfig(raw json.RawMessage) (conditionConfig, error) {
	var config conditionConfig
	if err := decodeConfig(raw, &config); err != nil {
		return conditionConfig{}, err
	}
	switch config.Operator {
	case "equals", "notEquals", "contains", "greaterThan", "lessThan", "isEmpty":
		return config, nil
	default:
		return conditionConfig{}, fmt.Errorf("%w: unsupported condition operator %q", ErrInvalidConfig, config.Operator)
	}
}

func evaluateCondition(operator string, value, compare any) (bool, error) {
	switch operator {
	case "equals":
		return reflect.DeepEqual(value, compare), nil
	case "notEquals":
		return !reflect.DeepEqual(value, compare), nil
	case "contains":
		if text, ok := value.(string); ok {
			needle, ok := compare.(string)
			if !ok {
				return false, ErrConditionTypeMismatch
			}
			return strings.Contains(text, needle), nil
		}
		valueReflect := reflect.ValueOf(value)
		if valueReflect.IsValid() && (valueReflect.Kind() == reflect.Slice || valueReflect.Kind() == reflect.Array) {
			for index := 0; index < valueReflect.Len(); index++ {
				if reflect.DeepEqual(valueReflect.Index(index).Interface(), compare) {
					return true, nil
				}
			}
			return false, nil
		}
		return false, ErrConditionTypeMismatch
	case "greaterThan", "lessThan":
		left, leftOK := numericValue(value)
		right, rightOK := numericValue(compare)
		if !leftOK || !rightOK {
			return false, ErrConditionTypeMismatch
		}
		if operator == "greaterThan" {
			return left > right, nil
		}
		return left < right, nil
	case "isEmpty":
		if value == nil {
			return true, nil
		}
		reflected := reflect.ValueOf(value)
		switch reflected.Kind() {
		case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
			return reflected.Len() == 0, nil
		default:
			return false, ErrConditionTypeMismatch
		}
	default:
		return false, fmt.Errorf("%w: unsupported condition operator %q", ErrInvalidConfig, operator)
	}
}

func numericValue(value any) (float64, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return 0, false
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(reflected.Uint()), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return 0, false
	}
}
