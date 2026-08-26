package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodeindex"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type recordingShutdownCoordinator struct {
	events *[]string
}

type recordingShutdownSupervisor struct {
	events *[]string
}

func (supervisor recordingShutdownSupervisor) BeginShutdown() {
	*supervisor.events = append(*supervisor.events, "supervisor-begin")
}

func (supervisor recordingShutdownSupervisor) Wait(context.Context) error {
	*supervisor.events = append(*supervisor.events, "supervisor-wait")
	return nil
}

func (coordinator recordingShutdownCoordinator) BeginShutdown() {
	*coordinator.events = append(*coordinator.events, "coordinator-begin")
}

func TestShutdownRuntimeCancelsRunsBeforeHTTPAndStopsLoopLast(t *testing.T) {
	events := []string{}
	done := make(chan error, 1)
	stop := func() {
		events = append(events, "coordinator-stop")
		done <- nil
	}
	shutdownHTTP := func(context.Context) error {
		events = append(events, "http-shutdown")
		return nil
	}
	err := shutdownRuntime(
		context.Background(),
		recordingShutdownSupervisor{events: &events},
		recordingShutdownCoordinator{events: &events},
		shutdownHTTP, stop, done,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"supervisor-begin", "coordinator-begin", "http-shutdown", "supervisor-wait", "coordinator-stop"}
	if len(events) != len(want) {
		t.Fatalf("shutdown events=%v", events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("shutdown events=%v, want %v", events, want)
		}
	}
}

func TestNodeIndexCatalogStartsFromEmbeddedWhenCacheIsMissing(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "missing")
	catalog, err := openNodePackageCatalog(cacheDir, buildinfo.Info{
		Version: "v0.3.0", APIVersion: "agent-studio.dev/v1alpha1",
	}, nodeindex.OpenStore)
	if err != nil {
		t.Fatal(err)
	}
	status := catalog.Status()
	if status.Source != nodeindex.SourceEmbedded || status.RuntimeVersion != "v0.3.0" || status.NodeAPI != "agent-studio.dev/v1alpha1" {
		t.Fatalf("status=%+v", status)
	}
}

func TestNodeIndexCatalogPropagatesStoreConstructionFailure(t *testing.T) {
	wantErr := errors.New("invalid embedded node index")
	_, err := openNodePackageCatalog("/unused", buildinfo.Info{}, func(string) (*nodeindex.Store, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "open node index") {
		t.Fatalf("error=%v", err)
	}
}

func TestRegisterCorePackageIsAtomicWhenMiddleStepFails(t *testing.T) {
	registry := nodes.NewRegistry()
	wantErr := errors.New("middle registration failed")
	record := nodepackage.RuntimeRecord{
		Summary: nodepackage.Summary{Name: "agent-studio.dev/core", Source: nodepackage.SourceBuiltin},
		Nodes:   []nodepackage.NodeRef{{Type: "start", Version: "1"}},
	}
	err := registerCorePackage(registry, record,
		func(registrar agentnode.Registrar) error { return registrar.Register(builtin.NewStart()) },
		func(agentnode.Registrar) error { return wantErr },
	)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "register core package") || len(registry.Definitions()) != 0 {
		t.Fatalf("error=%v definitions=%+v", err, registry.Definitions())
	}
}

func TestRegisterExtensionNodesAddsEcho(t *testing.T) {
	registry := nodes.NewRegistry()
	if err := registerExtensionNodes(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("extension.echo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := registerExtensionNodes(registry); err == nil {
		t.Fatal("duplicate extension registration must fail")
	}
}

func TestLogBuildInfoIncludesStableFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logBuildInfo(logger, buildinfo.Info{
		Version: "v0.2.0-rc.1", SDKVersion: "0.2.0",
		APIVersion: "agent-studio.dev/v1alpha1",
		Revision:   "abc123", Dirty: true,
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"msg": "server starting", "version": "v0.2.0-rc.1",
		"sdk_version": "0.2.0", "api_version": "agent-studio.dev/v1alpha1",
		"commit": "abc123", "dirty": true,
	}
	for key, value := range want {
		if event[key] != value {
			t.Fatalf("event[%q]=%#v, want %#v", key, event[key], value)
		}
	}
}
