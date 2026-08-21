package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenAIUsesConfiguredEndpointAndBearerToken(t *testing.T) {
	var path, auth string
	var rawRequestBody []byte
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path, auth = request.URL.Path, request.Header.Get("Authorization")
		var err error
		rawRequestBody, err = io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		if err := json.Unmarshal(rawRequestBody, &requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	}))
	defer server.Close()

	provider := NewOpenAICompatible(server.URL+"/v1/", "secret", server.Client())
	got, err := provider.Complete(context.Background(), Request{
		Model:        "gpt-test",
		SystemPrompt: "system",
		Prompt:       "hi",
		Temperature:  0.2,
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/chat/completions" || auth != "Bearer secret" || got.Text != "ok" {
		t.Fatalf("path=%s auth=%s got=%+v", path, auth, got)
	}
	const wantLegacyPayload = `{"model":"gpt-test","messages":[{"role":"system","content":"system"},{"role":"user","content":"hi"}],"temperature":0.2,"max_tokens":64}`
	if string(rawRequestBody) != wantLegacyPayload {
		t.Fatalf("legacy payload=%s", rawRequestBody)
	}
	if _, exists := requestBody["response_format"]; exists {
		t.Fatalf("legacy request unexpectedly contains response_format: %v", requestBody)
	}
	if requestBody["model"] != "gpt-test" || requestBody["max_tokens"] != float64(64) {
		t.Fatalf("request=%v", requestBody)
	}
	messages := requestBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages=%v", messages)
	}
}

func TestOpenAISendsNativeStructuredOutput(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":"{\"answer\":\"ok\"}"}}],"usage":{"total_tokens":3}}`)
	}))
	defer server.Close()

	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)
	got, err := NewOpenAICompatible(server.URL, "", server.Client()).Complete(context.Background(), Request{
		Model:  "gpt-structured",
		Prompt: "answer",
		ResponseFormat: &JSONSchemaFormat{
			Name:        "agent_studio_abcd",
			Description: "Agent Studio structured output",
			Schema:      schema,
			Strict:      true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != `{"answer":"ok"}` || got.Usage["totalTokens"] != 3 {
		t.Fatalf("response=%+v", got)
	}
	responseFormat, ok := requestBody["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format=%v", requestBody["response_format"])
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	if !ok || jsonSchema["name"] != "agent_studio_abcd" || jsonSchema["description"] != "Agent Studio structured output" || jsonSchema["strict"] != true {
		t.Fatalf("json_schema=%v", responseFormat["json_schema"])
	}
	wantSchema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"answer": map[string]any{"type": "string"}},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(jsonSchema["schema"], wantSchema) {
		t.Fatalf("schema=%v", jsonSchema["schema"])
	}
}

func TestOpenAIReportsRefusalWithoutLeakingContent(t *testing.T) {
	const refusal = "sensitive refusal detail"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"refusal":"`+refusal+`"}}]}`)
	}))
	defer server.Close()

	_, err := NewOpenAICompatible(server.URL, "", server.Client()).Complete(context.Background(), Request{Model: "test", Prompt: "hi"})
	if !errors.Is(err, ErrModelRefused) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), refusal) {
		t.Fatalf("error leaked refusal: %v", err)
	}
}

func TestOpenAIRejectsInvalidAndOversizedResponses(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{name: "no choices", body: `{"choices":[]}`, wantErr: ErrInvalidResponse},
		{name: "oversized", body: strings.Repeat("x", 1<<20+1), wantErr: ErrResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			_, err := NewOpenAICompatible(server.URL, "", server.Client()).Complete(context.Background(), Request{Model: "test", Prompt: "hi"})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestOpenAINonSuccessIsSafeAndContextCancellationPropagates(t *testing.T) {
	releaseSlowRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow/chat/completions" {
			<-releaseSlowRequest
			return
		}
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, `{"error":{"message":"upstream failed"}}`)
	}))
	defer server.Close()
	defer close(releaseSlowRequest)

	_, err := NewOpenAICompatible(server.URL, "super-secret", server.Client()).Complete(context.Background(), Request{Model: "test", Prompt: "hi"})
	var providerError *ProviderError
	if !errors.As(err, &providerError) || providerError.StatusCode != http.StatusBadGateway {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error leaked API key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = NewOpenAICompatible(server.URL+"/slow", "", server.Client()).Complete(ctx, Request{Model: "test", Prompt: "hi"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation error=%v", err)
	}
}
