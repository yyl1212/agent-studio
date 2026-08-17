package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxResponseBytes = 1 << 20

type OpenAICompatible struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func NewOpenAICompatible(baseURL, apiKey string, client *http.Client) *OpenAICompatible {
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICompatible{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  client,
	}
}

func (provider *OpenAICompatible) Complete(ctx context.Context, request Request) (Response, error) {
	messages := make([]openAIMessage, 0, 2)
	if request.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: request.SystemPrompt})
	}
	messages = append(messages, openAIMessage{Role: "user", Content: request.Prompt})
	payload, err := json.Marshal(openAIRequest{
		Model:       request.Model,
		Messages:    messages,
		Temperature: request.Temperature,
		MaxTokens:   request.MaxTokens,
	})
	if err != nil {
		return Response{}, fmt.Errorf("encode model request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("create model request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if provider.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+provider.apiKey)
	}

	httpResponse, err := provider.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("send model request: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxResponseBytes+1))
	if err != nil {
		return Response{}, fmt.Errorf("read model response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Response{}, ErrResponseTooLarge
	}
	if httpResponse.StatusCode < http.StatusOK || httpResponse.StatusCode >= http.StatusMultipleChoices {
		return Response{}, &ProviderError{StatusCode: httpResponse.StatusCode}
	}

	var decoded openAIResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, fmt.Errorf("%w: malformed JSON", ErrInvalidResponse)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, fmt.Errorf("%w: choices is empty", ErrInvalidResponse)
	}
	return Response{
		Text: decoded.Choices[0].Message.Content,
		Usage: map[string]int{
			"promptTokens":     decoded.Usage.PromptTokens,
			"completionTokens": decoded.Usage.CompletionTokens,
			"totalTokens":      decoded.Usage.TotalTokens,
		},
	}, nil
}
