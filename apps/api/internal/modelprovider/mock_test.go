package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMockIsDeterministic(t *testing.T) {
	provider := NewMock()
	got, err := provider.Complete(context.Background(), Request{Model: "mock", Prompt: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Mock 回复：你好" {
		t.Fatalf("text=%q", got.Text)
	}
}

func TestMockGeneratesDeterministicStructuredOutput(t *testing.T) {
	schema := json.RawMessage(`{
      "type":"object",
      "properties":{
        "name":{"type":"string"},
        "age":{"type":["integer","null"]},
        "score":{"type":"number"},
        "active":{"type":"boolean"},
        "tags":{"type":"array","items":{"type":"string"}}
      },
      "required":["name","age","score","active","tags"],
      "additionalProperties":false
    }`)
	got, err := NewMock().Complete(context.Background(), Request{
		Model:  "mock",
		Prompt: "你好",
		ResponseFormat: &JSONSchemaFormat{
			Name:   "agent_studio_test",
			Schema: schema,
			Strict: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != `{"active":false,"age":null,"name":"Mock 回复：你好","score":0,"tags":[]}` {
		t.Fatalf("text=%s", got.Text)
	}
}

func TestMockRejectsUnsupportedStructuredSchema(t *testing.T) {
	_, err := NewMock().Complete(context.Background(), Request{
		ResponseFormat: &JSONSchemaFormat{
			Name:   "bad",
			Schema: json.RawMessage(`{"type":"array"}`),
			Strict: true,
		},
	})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error=%v", err)
	}
}
