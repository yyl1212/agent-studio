package main

import (
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
)

func TestRegisterExtensionNodesAddsEcho(t *testing.T) {
	registry := nodes.NewRegistry()
	if err := registerExtensionNodes(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("extension.echo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
}
