package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestConditionSupportsAllOperators(t *testing.T) {
	tests := []struct {
		name       string
		operator   string
		value      any
		compare    any
		activePort string
	}{
		{name: "equals", operator: "equals", value: "yes", compare: "yes", activePort: "true"},
		{name: "not equals", operator: "notEquals", value: "yes", compare: "no", activePort: "true"},
		{name: "contains", operator: "contains", value: "agent studio", compare: "studio", activePort: "true"},
		{name: "greater", operator: "greaterThan", value: 4.0, compare: 3.0, activePort: "true"},
		{name: "less", operator: "lessThan", value: 2.0, compare: 3.0, activePort: "true"},
		{name: "empty", operator: "isEmpty", value: []any{}, activePort: "true"},
		{name: "false branch", operator: "equals", value: "yes", compare: "no", activePort: "false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := json.Marshal(map[string]any{"operator": test.operator, "compareValue": test.compare})
			if err != nil {
				t.Fatal(err)
			}
			result, err := NewCondition().Execute(context.Background(), domainRequest(config, map[string][]any{"value": {test.value}}))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.ActivePorts) != 1 || result.ActivePorts[0] != test.activePort {
				t.Fatalf("active ports=%v", result.ActivePorts)
			}
			if !reflect.DeepEqual(result.Outputs[test.activePort], test.value) {
				t.Fatalf("output=%v", result.Outputs)
			}
		})
	}
}

func TestConditionRejectsIncompatibleTypes(t *testing.T) {
	cfg := json.RawMessage(`{"operator":"greaterThan","compareValue":2}`)
	_, err := NewCondition().Execute(context.Background(), domainRequest(cfg, map[string][]any{"value": {"three"}}))
	if !errors.Is(err, ErrConditionTypeMismatch) {
		t.Fatalf("error=%v", err)
	}
}
