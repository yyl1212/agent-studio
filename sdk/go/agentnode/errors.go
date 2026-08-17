package agentnode

import (
	"context"
	"errors"
	"fmt"
)

type ErrorKind string

const (
	ErrorKindConfig    ErrorKind = "config"
	ErrorKindInput     ErrorKind = "input"
	ErrorKindTemporary ErrorKind = "temporary"
	ErrorKindCanceled  ErrorKind = "canceled"
	ErrorKindInternal  ErrorKind = "internal"
)

type NodeError struct {
	Kind    ErrorKind
	Code    string
	Details map[string]any
	Err     error
}

func NewError(kind ErrorKind, code string, err error, details map[string]any) *NodeError {
	return &NodeError{Kind: kind, Code: code, Details: details, Err: err}
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Code)
}

func (e *NodeError) Unwrap() error {
	return e.Err
}

func KindOf(err error) ErrorKind {
	var nodeErr *NodeError
	if errors.As(err, &nodeErr) {
		return nodeErr.Kind
	}
	if errors.Is(err, context.Canceled) {
		return ErrorKindCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorKindTemporary
	}
	return ErrorKindInternal
}
