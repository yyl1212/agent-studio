package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/modelprovider"
	"agentstudio.local/api/internal/nodes"
)

type recordingProvider struct {
	request modelprovider.Request
}

func (provider *recordingProvider) Complete(ctx context.Context, request modelprovider.Request) (modelprovider.Response, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 61*time.Second || time.Until(deadline) < 55*time.Second {
		return modelprovider.Response{}, errors.New("LLM context does not have a 60 second deadline")
	}
	provider.request = request
	return modelprovider.Response{Text: "answer", Usage: map[string]int{"promptTokens": 1}}, nil
}

func TestLLMUsesInjectedProviderAndDefaultModel(t *testing.T) {
	provider := &recordingProvider{}
	node := NewLLM(provider, "default-model")
	config := json.RawMessage(`{"systemPrompt":"system","temperature":0.3,"maxTokens":128}`)

	result, err := node.Execute(context.Background(), domain.NodeRequest{
		Config: config,
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.request.Model != "default-model" || provider.request.Prompt != "hello" || provider.request.SystemPrompt != "system" {
		t.Fatalf("request=%+v", provider.request)
	}
	if result.Outputs["text"] != "answer" {
		t.Fatalf("outputs=%v", result.Outputs)
	}
}

func TestLLMRequiresModelAndRejectsAPIKeyConfig(t *testing.T) {
	provider := &recordingProvider{}
	_, err := NewLLM(provider, "").Execute(context.Background(), domain.NodeRequest{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if !errors.Is(err, ErrLLMModelMissing) {
		t.Fatalf("model error=%v", err)
	}

	registry := nodes.NewRegistry()
	if err := registry.Register(NewLLM(provider, "default")); err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateConfig("llm", "1", json.RawMessage(`{"apiKey":"secret"}`)); err == nil {
		t.Fatal("expected API key config to be rejected")
	}
}
