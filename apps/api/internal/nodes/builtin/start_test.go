package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestStartResolvesFieldsAndEmitsRunInput(t *testing.T) {
	cfg := json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true},{"key":"detail","label":"详情","type":"textarea"},{"key":"count","label":"数量","type":"number"},{"key":"enabled","label":"启用","type":"boolean"},{"key":"payload","label":"数据","type":"json"}]}`)
	node := NewStart()

	ports, err := node.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []domain.DataType{domain.TypeString, domain.TypeString, domain.TypeNumber, domain.TypeBoolean, domain.TypeJSON}
	for index, want := range wantTypes {
		if ports.Outputs[index].Type != want {
			t.Fatalf("output %d type=%q, want %q", index, ports.Outputs[index].Type, want)
		}
	}

	result, err := node.Execute(context.Background(), domain.NodeRequest{
		Config: cfg,
		RunInput: map[string]any{
			"topic":   "Agent",
			"count":   2.0,
			"enabled": true,
			"payload": map[string]any{"kind": "demo"},
			"unknown": "discard me",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outputs["topic"] != "Agent" {
		t.Fatalf("topic=%v", result.Outputs["topic"])
	}
	if _, exists := result.Outputs["unknown"]; exists {
		t.Fatalf("undefined field leaked into outputs: %v", result.Outputs)
	}
}

func TestStartRejectsInvalidAndDuplicateFieldKeys(t *testing.T) {
	tests := []struct {
		name   string
		config string
	}{
		{name: "invalid", config: `{"fields":[{"key":"bad-key","label":"坏字段","type":"text"}]}`},
		{name: "duplicate", config: `{"fields":[{"key":"topic","label":"主题","type":"text"},{"key":"topic","label":"重复","type":"text"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewStart().Resolve(json.RawMessage(test.config)); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestStartAppliesDefaultsAndValidatesSelectAndRequired(t *testing.T) {
	cfg := json.RawMessage(`{"fields":[{"key":"tone","label":"语气","type":"select","required":true,"default":"brief","options":["brief","detailed"]},{"key":"topic","label":"主题","type":"text","required":true}]}`)
	node := NewStart()

	result, err := node.Execute(context.Background(), domain.NodeRequest{Config: cfg, RunInput: map[string]any{"topic": "Agent"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outputs["tone"] != "brief" {
		t.Fatalf("default tone=%v", result.Outputs["tone"])
	}

	_, err = node.Execute(context.Background(), domain.NodeRequest{Config: cfg, RunInput: map[string]any{"topic": "Agent", "tone": "invalid"}})
	if !errors.Is(err, ErrInputTypeMismatch) {
		t.Fatalf("select error=%v", err)
	}
	_, err = node.Execute(context.Background(), domain.NodeRequest{Config: cfg, RunInput: map[string]any{}})
	if !errors.Is(err, ErrRequiredInputMissing) {
		t.Fatalf("required error=%v", err)
	}
}

func TestDeriveInputSchemaPreservesStartFieldPresentation(t *testing.T) {
	cfg := json.RawMessage(`{"fields":[{"key":"topic","label":"主题","description":"要研究的主题","type":"textarea","required":true,"default":"Agent","placeholder":"请输入主题"},{"key":"tone","label":"语气","type":"select","options":["brief","detailed"]}]}`)

	raw, err := DeriveInputSchema(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties=%v", schema["additionalProperties"])
	}
	if !reflect.DeepEqual(schema["required"], []any{"topic"}) {
		t.Fatalf("required=%v", schema["required"])
	}
	properties := schema["properties"].(map[string]any)
	topic := properties["topic"].(map[string]any)
	if topic["title"] != "主题" || topic["x-ui-widget"] != "textarea" || topic["x-ui-placeholder"] != "请输入主题" {
		t.Fatalf("topic schema=%v", topic)
	}
	tone := properties["tone"].(map[string]any)
	if !reflect.DeepEqual(tone["enum"], []any{"brief", "detailed"}) || tone["x-ui-widget"] != "select" {
		t.Fatalf("tone schema=%v", tone)
	}
}
