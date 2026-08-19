package builtin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func TestRuntimeRecordListsExactCoreNodeSet(t *testing.T) {
	record := RuntimeRecord(buildinfo.Info{Version: "v0.3.0"})
	want := []string{"start@1", "template@1", "condition@1", "end@1", "llm@1", "http@1", "code@1"}
	got := make([]string, 0, len(record.Nodes))
	for _, node := range record.Nodes {
		got = append(got, node.Type+"@"+node.Version)
	}
	if !reflect.DeepEqual(got, want) || record.Summary.Name != "agent-studio.dev/core" || record.Summary.Source != "builtin" {
		t.Fatalf("record=%+v", record)
	}
}

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
