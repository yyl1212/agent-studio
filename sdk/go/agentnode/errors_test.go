package agentnode_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestNodeErrorPreservesCauseWithoutExposingItsText(t *testing.T) {
	cause := errors.New("top-secret")
	details := map[string]any{"field": "prompt"}
	err := agentnode.NewError(agentnode.ErrorKindInput, "missing_input", cause, details)

	if got, want := err.Error(), "input: missing_input"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is must find the wrapped cause")
	}
	var nodeErr *agentnode.NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatal("errors.As must find NodeError")
	}
	if nodeErr.Kind != agentnode.ErrorKindInput || nodeErr.Code != "missing_input" {
		t.Fatalf("node error = %#v", nodeErr)
	}
	if !reflect.DeepEqual(nodeErr.Details, details) {
		t.Fatalf("details = %#v, want %#v", nodeErr.Details, details)
	}
}

func TestKindOfClassifiesNodeAndContextErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want agentnode.ErrorKind
	}{
		{name: "config", err: agentnode.NewError(agentnode.ErrorKindConfig, "invalid_config", nil, nil), want: agentnode.ErrorKindConfig},
		{name: "input", err: agentnode.NewError(agentnode.ErrorKindInput, "invalid_input", nil, nil), want: agentnode.ErrorKindInput},
		{name: "temporary", err: agentnode.NewError(agentnode.ErrorKindTemporary, "upstream_timeout", nil, nil), want: agentnode.ErrorKindTemporary},
		{name: "canceled node error", err: agentnode.NewError(agentnode.ErrorKindCanceled, "run_canceled", nil, nil), want: agentnode.ErrorKindCanceled},
		{name: "internal", err: agentnode.NewError(agentnode.ErrorKindInternal, "execution_failed", nil, nil), want: agentnode.ErrorKindInternal},
		{name: "context canceled", err: context.Canceled, want: agentnode.ErrorKindCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: agentnode.ErrorKindTemporary},
		{name: "unknown", err: errors.New("unknown"), want: agentnode.ErrorKindInternal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := agentnode.KindOf(test.err); got != test.want {
				t.Fatalf("KindOf() = %q, want %q", got, test.want)
			}
		})
	}
}
