package generated

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestRegisterNodesAddsEcho(t *testing.T) {
	registry := nodes.NewRegistry()
	if err := RegisterNodes(registry); err != nil {
		t.Fatal(err)
	}
	node, err := registry.Get("extension.echo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	result, err := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"prefix":"回答："}`),
		Inputs: map[string][]any{"text": {"你好"}},
	})
	if err != nil || result.Outputs["text"] != "回答：你好" {
		t.Fatalf("result=%v err=%v", result, err)
	}
}
