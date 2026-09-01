package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func TestBuildCommonRejectsCipherBeforeExternalInitialization(t *testing.T) {
	_, err := BuildCommon(context.Background(), config.Config{RunPayloadEncryptionKey: "not-a-key"}, buildinfo.Info{}, slog.Default())
	if err == nil || err.Error() != "initialize run payload cipher: invalid configuration" {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildAPIRejectsIncompleteCommonDependencies(t *testing.T) {
	if _, err := BuildAPI(&Common{}, slog.Default()); err == nil || err.Error() != "API bootstrap dependencies are incomplete" {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildRegistryContainsCoreAndExtensionNodes(t *testing.T) {
	registry, err := buildRegistry(config.Config{ModelProvider: "mock"}, buildinfo.Current())
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range []struct{ nodeType, version string }{{"start", "1"}, {"end", "1"}, {"extension.echo", "1.0.0"}} {
		if _, err := registry.Get(node.nodeType, node.version); err != nil {
			t.Fatalf("missing %s@%s: %v", node.nodeType, node.version, err)
		}
	}
}
