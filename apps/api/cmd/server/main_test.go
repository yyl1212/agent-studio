package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/internal/buildinfo"
	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

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
