package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestTemplateResolvesUniqueVariablesAndRendersJSONCompactly(t *testing.T) {
	cfg := json.RawMessage(`{"template":"主题：{{topic}}；再次：{{topic}}；参数：{{payload}}"}`)
	node := NewTemplate()

	ports, err := node.Resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	gotKeys := []string{ports.Inputs[0].Key, ports.Inputs[1].Key}
	if want := []string{"topic", "payload"}; !reflect.DeepEqual(gotKeys, want) {
		t.Fatalf("input keys=%v, want %v", gotKeys, want)
	}

	result, err := node.Execute(context.Background(), domainRequest(cfg, map[string][]any{
		"topic":   {"Agent"},
		"payload": {map[string]any{"count": 2.0}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Outputs["text"], `主题：Agent；再次：Agent；参数：{"count":2}`; got != want {
		t.Fatalf("text=%q, want %q", got, want)
	}
}

func TestTemplateRejectsMissingVariableAndInvalidPlaceholder(t *testing.T) {
	node := NewTemplate()
	cfg := json.RawMessage(`{"template":"主题：{{topic}}"}`)
	_, err := node.Execute(context.Background(), domainRequest(cfg, map[string][]any{}))
	if !errors.Is(err, ErrRequiredInputMissing) {
		t.Fatalf("missing variable error=%v", err)
	}

	_, err = node.Resolve(json.RawMessage(`{"template":"主题：{{ topic }}"}`))
	if !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("invalid placeholder error=%v", err)
	}
}
