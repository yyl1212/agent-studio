package bootstrap

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/config"
	workerprocess "github.com/yyl1212/agent-studio/apps/api/internal/worker"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func TestBuildCommonRejectsCipherBeforeExternalInitialization(t *testing.T) {
	_, err := BuildCommon(context.Background(), config.Config{RunPayloadEncryptionKey: "not-a-key"}, buildinfo.Info{}, slog.Default())
	if err == nil || err.Error() != "initialize run payload cipher: invalid configuration" {
		t.Fatalf("error=%v", err)
	}
}

func TestDurableWorkerConfigMapsEveryField(t *testing.T) {
	cfg := config.Config{
		WorkerMaxActiveRuns:       7,
		WorkerLeaseDuration:       11 * time.Second,
		WorkerHeartbeatInterval:   12 * time.Second,
		WorkerClaimInterval:       13 * time.Second,
		WorkerQueueSampleInterval: 14 * time.Second,
		WorkerShutdownTimeout:     15 * time.Second,
	}

	got := durableWorkerConfig(cfg, "worker-42")
	want := workerprocess.Config{
		OwnerID:             "worker-42",
		MaxActiveRuns:       7,
		LeaseDuration:       11 * time.Second,
		HeartbeatInterval:   12 * time.Second,
		ClaimInterval:       13 * time.Second,
		QueueSampleInterval: 14 * time.Second,
		ShutdownTimeout:     15 * time.Second,
	}
	if got != want {
		t.Fatalf("durableWorkerConfig() = %+v, want %+v", got, want)
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
