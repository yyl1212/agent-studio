package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/apps/api/internal/runpayload"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestWorkerTelemetryUsesOnlyBoundedLabels(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	telemetry := newTelemetry(observability.Providers{MeterProvider: provider})
	ctx := context.Background()
	telemetry.claim(ctx, "sentinel-database-url", time.Millisecond)
	telemetry.queue(ctx, 2, time.Second)
	telemetry.queueSample(ctx, "sentinel-private-value")
	telemetry.leaseStarted(ctx, true)
	telemetry.renewal(ctx, "sentinel-ciphertext")
	telemetry.fenced(ctx)
	telemetry.autoRecoveries.Add(ctx, 1)
	telemetry.recovery(ctx, RecoveryDecision{
		Required: true, Reason: domain.RecoveryPayloadUnavailable, PayloadFailureCategory: "sentinel-private-input",
		Nodes: []UncertainNode{{NodeID: "sentinel-node-id", Safety: agentnode.ExecutionSafety("sentinel-safety")}},
	})
	telemetry.leaseFinished(ctx)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(collected)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"sentinel-database-url", "sentinel-ciphertext", "sentinel-private-input", "sentinel-private-value", "sentinel-node-id", "sentinel-safety"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("telemetry leaked %q: %s", forbidden, body)
		}
	}
	for _, metricName := range []string{
		"agent_studio.worker.run_claim_total", "agent_studio.worker.claim_latency", "agent_studio.worker.queue_depth",
		"agent_studio.worker.oldest_queued_age", "agent_studio.worker.active_leases", "agent_studio.worker.lease_renew_total",
		"agent_studio.worker.expired_lease_reclaim_total", "agent_studio.worker.fencing_rejected_total",
		"agent_studio.worker.auto_recovery_total", "agent_studio.worker.run_recovery_required_total",
		"agent_studio.worker.payload_decrypt_failure_total", "agent_studio.worker.queue_sample_total",
	} {
		if !strings.Contains(string(body), metricName) {
			t.Fatalf("missing metric %q: %s", metricName, body)
		}
	}
}

func TestPayloadFailureCategoryIsStable(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{err: nil, want: "unknown"},
		{err: errors.New("payload missing"), want: "missing"},
		{err: errors.New("payload json invalid"), want: "json"},
		{err: runpayload.ErrAuthentication, want: "authentication"},
		{err: runpayload.ErrInvalidEnvelope, want: "envelope"},
		{err: errors.New("top-secret-error"), want: "unknown"},
	} {
		if got := payloadFailureCategory(test.err); got != test.want {
			t.Fatalf("category(%v)=%q want=%q", test.err, got, test.want)
		}
	}
}
