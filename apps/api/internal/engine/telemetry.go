package engine

import (
	"context"
	"errors"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var nodeDurationBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120,
}

var errNodeExecutionPanic = errors.New("node execution panicked")

type nodeTelemetry struct {
	tracer     trace.Tracer
	executions metric.Int64Counter
	duration   metric.Float64Histogram
	failures   metric.Int64Counter
}

func newNodeTelemetry(providers observability.Providers) *nodeTelemetry {
	meter := providers.Meter("agent-studio/engine")
	executions, _ := meter.Int64Counter("agent_studio.workflow.node.executions")
	duration, _ := meter.Float64Histogram(
		"agent_studio.workflow.node.duration",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(nodeDurationBoundaries...),
	)
	failures, _ := meter.Int64Counter("agent_studio.workflow.node.failures")
	return &nodeTelemetry{
		tracer:     providers.Tracer("agent-studio/engine"),
		executions: executions, duration: duration, failures: failures,
	}
}

func (telemetry *nodeTelemetry) start(ctx context.Context, compiled CompiledNode) (context.Context, func(string, observability.ErrorCategory)) {
	safety := agentnode.EffectiveExecutionSafety(compiled.ExecutionSafety)
	ctx, span := telemetry.tracer.Start(ctx, "workflow.node", trace.WithAttributes(
		attribute.String("agent_studio.node.id", compiled.Node.ID),
		attribute.String("agent_studio.node.type", compiled.Node.Type),
		attribute.String("agent_studio.node.type_version", compiled.Node.TypeVersion),
		attribute.String("agent_studio.node.execution_safety", string(safety)),
	))
	started := time.Now()
	return ctx, func(status string, category observability.ErrorCategory) {
		completedAttributes := []attribute.KeyValue{
			attribute.String("node_type", compiled.Node.Type),
			attribute.String("status", status),
			attribute.String("execution_safety", string(safety)),
		}
		telemetry.executions.Add(ctx, 1, metric.WithAttributes(completedAttributes...))
		telemetry.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(completedAttributes...))
		span.SetAttributes(attribute.String("agent_studio.node.status", status))
		if category != "" {
			telemetry.failures.Add(ctx, 1, metric.WithAttributes(
				attribute.String("node_type", compiled.Node.Type),
				attribute.String("execution_safety", string(safety)),
				attribute.String("error_category", string(category)),
			))
			span.SetAttributes(attribute.String("error.category", string(category)))
			span.SetStatus(codes.Error, string(category))
		}
		span.End()
	}
}

func classifyNodeError(err error) observability.ErrorCategory {
	if err == nil {
		return ""
	}
	if category := observability.ContextErrorCategory(err); category != observability.ErrorCategoryInternal {
		return category
	}
	return observability.ErrorCategoryNodeExecution
}

func nodeTelemetryStatus(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return string(domain.NodeCancelled)
	}
	if err != nil {
		return string(domain.NodeFailed)
	}
	return string(domain.NodeCompleted)
}
