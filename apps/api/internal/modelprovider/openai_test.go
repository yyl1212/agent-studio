package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAIUsesConfiguredEndpointAndBearerToken(t *testing.T) {
	var path, auth string
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path, auth = request.URL.Path, request.Header.Get("Authorization")
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
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
	if requestBody["model"] != "gpt-test" || requestBody["max_tokens"] != float64(64) {
		t.Fatalf("request=%v", requestBody)
	}
	messages := requestBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages=%v", messages)
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
