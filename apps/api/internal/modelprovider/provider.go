package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidResponse  = errors.New("invalid model provider response")
	ErrModelRefused     = errors.New("model refused to generate a response")
	ErrResponseTooLarge = errors.New("model provider response too large")
)

type JSONSchemaFormat struct {
	Name        string
	Description string
	Schema      json.RawMessage
	Strict      bool
}

type Request struct {
	Model          string
	SystemPrompt   string
	Prompt         string
	Temperature    float64
	MaxTokens      int
	ResponseFormat *JSONSchemaFormat
}

type Response struct {
	Text  string
	Usage map[string]int
}

type Provider interface {
	Complete(context.Context, Request) (Response, error)
}

type ProviderError struct {
	StatusCode int
}

func (err *ProviderError) Error() string {
	return fmt.Sprintf("model provider request failed with status %d", err.StatusCode)
}
