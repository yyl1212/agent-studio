package builtin

import (
	"context"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
)

func TestEndAcceptsExactlyOneActiveResult(t *testing.T) {
	node := NewEnd()
	ports, err := node.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if ports.Inputs[0].Cardinality != domain.CardinalitySingleActive {
		t.Fatalf("cardinality=%q", ports.Inputs[0].Cardinality)
	}

	result, err := node.Execute(context.Background(), domain.NodeRequest{Inputs: map[string][]any{"result": {"done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outputs["result"] != "done" {
		t.Fatalf("result=%v", result.Outputs)
	}
}

func TestEndRejectsZeroOrMultipleActiveResults(t *testing.T) {
	_, err := NewEnd().Execute(context.Background(), domain.NodeRequest{Inputs: map[string][]any{}})
	if !errors.Is(err, ErrEndResultMissing) {
		t.Fatalf("missing error=%v", err)
	}
	_, err = NewEnd().Execute(context.Background(), domain.NodeRequest{Inputs: map[string][]any{"result": {"a", "b"}}})
	if !errors.Is(err, ErrEndMultipleResults) {
		t.Fatalf("multiple error=%v", err)
	}
}

func TestRegisterCoreRegistersFourDefinitions(t *testing.T) {
	registry := nodes.NewRegistry()
	if err := RegisterCore(registry); err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 4 {
		t.Fatalf("definition count=%d", len(definitions))
	}
}
