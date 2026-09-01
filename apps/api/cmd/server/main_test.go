package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/internal/buildinfo"
)

func TestWaitForRuntimeStopFailsSafelyWhenMaintenanceLeaseIsLost(t *testing.T) {
	lost := make(chan error, 1)
	lost <- errors.New("postgres://secret maintenance connection lost")
	err := waitForRuntimeStop(make(chan error), make(chan struct{}), lost)
	if err == nil || err.Error() != "database maintenance lease lost" || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("error=%v", err)
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
