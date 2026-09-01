package worker

import (
	"context"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Telemetry struct {
	claims           metric.Int64Counter
	claimLatency     metric.Float64Histogram
	activeLeases     metric.Int64UpDownCounter
	renewals         metric.Int64Counter
	reclaimed        metric.Int64Counter
	fencingRejected  metric.Int64Counter
	autoRecoveries   metric.Int64Counter
	recoveryRequired metric.Int64Counter
	payloadFailures  metric.Int64Counter
	queueDepth       metric.Int64Gauge
	oldestQueued     metric.Float64Gauge
	queueSamples     metric.Int64Counter
}

func newTelemetry(providers observability.Providers) *Telemetry {
	meter := providers.Meter("agent-studio/worker")
	claims, _ := meter.Int64Counter("agent_studio.worker.run_claim_total")
	claimLatency, _ := meter.Float64Histogram("agent_studio.worker.claim_latency", metric.WithUnit("s"))
	activeLeases, _ := meter.Int64UpDownCounter("agent_studio.worker.active_leases")
	renewals, _ := meter.Int64Counter("agent_studio.worker.lease_renew_total")
	reclaimed, _ := meter.Int64Counter("agent_studio.worker.expired_lease_reclaim_total")
	fencingRejected, _ := meter.Int64Counter("agent_studio.worker.fencing_rejected_total")
	autoRecoveries, _ := meter.Int64Counter("agent_studio.worker.auto_recovery_total")
	recoveryRequired, _ := meter.Int64Counter("agent_studio.worker.run_recovery_required_total")
	payloadFailures, _ := meter.Int64Counter("agent_studio.worker.payload_decrypt_failure_total")
	queueDepth, _ := meter.Int64Gauge("agent_studio.worker.queue_depth")
	oldestQueued, _ := meter.Float64Gauge("agent_studio.worker.oldest_queued_age", metric.WithUnit("s"))
	queueSamples, _ := meter.Int64Counter("agent_studio.worker.queue_sample_total")
	return &Telemetry{
		claims: claims, claimLatency: claimLatency, activeLeases: activeLeases, renewals: renewals,
		reclaimed: reclaimed, fencingRejected: fencingRejected, autoRecoveries: autoRecoveries,
		recoveryRequired: recoveryRequired, payloadFailures: payloadFailures, queueDepth: queueDepth, oldestQueued: oldestQueued,
		queueSamples: queueSamples,
	}
}

func (telemetry *Telemetry) claim(ctx context.Context, outcome string, duration time.Duration) {
	outcome = boundedClaimOutcome(outcome)
	telemetry.claims.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
	telemetry.claimLatency.Record(ctx, duration.Seconds(), metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (telemetry *Telemetry) queue(ctx context.Context, depth int64, oldest time.Duration) {
	if depth < 0 {
		depth = 0
	}
	if oldest < 0 {
		oldest = 0
	}
	telemetry.queueDepth.Record(ctx, depth)
	telemetry.oldestQueued.Record(ctx, oldest.Seconds())
}

func (telemetry *Telemetry) queueSample(ctx context.Context, outcome string) {
	if outcome != "success" {
		outcome = "error"
	}
	telemetry.queueSamples.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (telemetry *Telemetry) leaseStarted(ctx context.Context, reclaimed bool) {
	telemetry.activeLeases.Add(ctx, 1)
	if reclaimed {
		telemetry.reclaimed.Add(ctx, 1)
	}
}

func (telemetry *Telemetry) leaseFinished(ctx context.Context) {
	telemetry.activeLeases.Add(ctx, -1)
}

func (telemetry *Telemetry) renewal(ctx context.Context, outcome string) {
	telemetry.renewals.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", boundedRenewalOutcome(outcome))))
}

func (telemetry *Telemetry) fenced(ctx context.Context) {
	telemetry.fencingRejected.Add(ctx, 1)
}

func (telemetry *Telemetry) recovery(ctx context.Context, decision RecoveryDecision) {
	if !decision.Required {
		return
	}
	reason := boundedRecoveryReason(decision.Reason)
	if decision.Reason == domain.RecoveryPayloadUnavailable {
		telemetry.payloadFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("category", boundedPayloadFailure(decision.PayloadFailureCategory))))
	}
	if len(decision.Nodes) == 0 {
		telemetry.recoveryRequired.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason), attribute.String("safety", "unknown")))
		return
	}
	seen := make(map[string]struct{})
	for _, node := range decision.Nodes {
		safety := boundedSafety(node.Safety)
		if _, ok := seen[safety]; ok {
			continue
		}
		seen[safety] = struct{}{}
		telemetry.recoveryRequired.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason), attribute.String("safety", safety)))
	}
}

func boundedClaimOutcome(value string) string {
	switch value {
	case "claimed", "empty", "error":
		return value
	default:
		return "error"
	}
}

func boundedRenewalOutcome(value string) string {
	switch value {
	case "success", "cancel_requested", "lease_lost", "error":
		return value
	default:
		return "error"
	}
}

func boundedRecoveryReason(reason domain.RunRecoveryReason) string {
	switch reason {
	case domain.RecoveryLegacyActive, domain.RecoveryUncertainReadOnly, domain.RecoveryUncertainEffect,
		domain.RecoveryAttemptLimit, domain.RecoveryHistoryInvalid, domain.RecoveryPayloadUnavailable,
		domain.RecoveryNodeUnavailable:
		return string(reason)
	default:
		return "unknown"
	}
}

func boundedSafety(safety agentnode.ExecutionSafety) string {
	switch safety {
	case agentnode.ExecutionSafetyPure, agentnode.ExecutionSafetyReadOnly, agentnode.ExecutionSafetySideEffect:
		return string(safety)
	default:
		return "unknown"
	}
}

func boundedPayloadFailure(value string) string {
	switch value {
	case "authentication", "envelope", "missing", "json", "metadata":
		return value
	default:
		return "unknown"
	}
}
