package modelprovider

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrInvalidResponse  = errors.New("invalid model provider response")
	ErrResponseTooLarge = errors.New("model provider response too large")
)

type Request struct {
	Model        string
	SystemPrompt string
	Prompt       string
	Temperature  float64
	MaxTokens    int
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
