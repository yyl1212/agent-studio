package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWorkerOwnerIDIsUniqueAndContainsNoConfiguration(t *testing.T) {
	first, second := workerOwnerID(), workerOwnerID()
	if first == second || strings.Contains(first, "postgres://") || strings.Contains(first, "RUN_PAYLOAD_ENCRYPTION_KEY") {
		t.Fatalf("unsafe worker IDs: %q %q", first, second)
	}
	parts := strings.Split(first, ":")
	if len(parts) != 3 {
		t.Fatalf("worker ID=%q", first)
	}
	if _, err := uuid.Parse(parts[2]); err != nil {
		t.Fatalf("worker ID UUID=%q error=%v", parts[2], err)
	}
}

func TestRunRejectsInvalidWorkerQueueSamplingBeforeExternalInitialization(t *testing.T) {
	t.Setenv("RUN_PAYLOAD_ENCRYPTION_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	t.Setenv("MODEL_PROVIDER", "mock")
	t.Setenv("DATABASE_URL", "postgres://agent:agent@127.0.0.1:1/agent_studio?sslmode=disable")
	t.Setenv("AGENT_STUDIO_NODE_INDEX_CACHE_DIR", "")
	t.Setenv("WORKER_MAX_ACTIVE_RUNS", "1")
	t.Setenv("WORKER_LEASE_DURATION", "30s")
	t.Setenv("WORKER_HEARTBEAT_INTERVAL", "10s")
	t.Setenv("WORKER_CLAIM_INTERVAL", "500ms")
	t.Setenv("WORKER_SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("RUN_EVENT_POLL_INTERVAL", "250ms")
	t.Setenv("WORKER_QUEUE_SAMPLE_INTERVAL", "sentinel-invalid")

	err := run(context.Background(), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "WORKER_QUEUE_SAMPLE_INTERVAL") || strings.Contains(err.Error(), "sentinel-invalid") {
		t.Fatalf("run() error = %v", err)
	}
}
