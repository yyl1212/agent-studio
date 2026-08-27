package workflow

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var runDurationBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120,
}

type runTelemetry struct {
	tracer   trace.Tracer
	runs     metric.Int64Counter
	duration metric.Float64Histogram
	active   metric.Int64UpDownCounter
}

type runOrigin struct {
	spanContext trace.SpanContext
	requestID   string
}

type runOriginContextKey struct{}

func contextWithRunOrigin(ctx context.Context, origin runOrigin) context.Context {
	return context.WithValue(ctx, runOriginContextKey{}, origin)
}

func newRunTelemetry(providers observability.Providers) *runTelemetry {
	meter := providers.Meter("agent-studio/workflow")
	runs, _ := meter.Int64Counter("agent_studio.workflow.runs")
	duration, _ := meter.Float64Histogram(
		"agent_studio.workflow.run.duration",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(runDurationBoundaries...),
	)
	active, _ := meter.Int64UpDownCounter("agent_studio.workflow.run.active")
	return &runTelemetry{
		tracer: providers.Tracer("agent-studio/workflow"),
		runs:   runs, duration: duration, active: active,
	}
}

func (telemetry *runTelemetry) start(ctx context.Context, prepared *PreparedRun) (context.Context, func(domain.RunStatus, observability.ErrorCategory)) {
	mode := normalizedRunMode(prepared)
	spanAttributes := []attribute.KeyValue{
		attribute.String("agent_studio.run.id", preparedRunID(prepared)),
		attribute.String("agent_studio.run.mode", mode),
	}
	if prepared != nil {
		spanAttributes = appendOptionalRunAttribute(spanAttributes, "agent_studio.run.retry_of_id", prepared.retryOfRunID)
		spanAttributes = appendOptionalRunAttribute(spanAttributes, "agent_studio.run.source_id", prepared.sourceRunID)
		spanAttributes = appendOptionalRunAttribute(spanAttributes, "agent_studio.run.source_node_id", prepared.sourceNodeID)
	}
	spanOptions := []trace.SpanStartOption{trace.WithAttributes(spanAttributes...)}
	if origin, ok := ctx.Value(runOriginContextKey{}).(runOrigin); ok {
		spanOptions = append(spanOptions, trace.WithNewRoot())
		if origin.spanContext.IsValid() {
			spanOptions = append(spanOptions, trace.WithLinks(trace.Link{SpanContext: origin.spanContext}))
		}
	}
	ctx, span := telemetry.tracer.Start(ctx, "workflow.run", spanOptions...)
	modeAttribute := attribute.String("mode", mode)
	telemetry.active.Add(ctx, 1, metric.WithAttributes(modeAttribute))
	started := time.Now()
	var finishOnce sync.Once
	return ctx, func(status domain.RunStatus, category observability.ErrorCategory) {
		finishOnce.Do(func() {
			statusValue := normalizedRunStatus(status)
			completedAttributes := []attribute.KeyValue{modeAttribute, attribute.String("status", statusValue)}
			telemetry.active.Add(ctx, -1, metric.WithAttributes(modeAttribute))
			telemetry.runs.Add(ctx, 1, metric.WithAttributes(completedAttributes...))
			telemetry.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(completedAttributes...))
			span.SetAttributes(attribute.String("agent_studio.run.status", statusValue))
			if category != "" {
				span.SetAttributes(attribute.String("error.category", string(category)))
				span.SetStatus(codes.Error, string(category))
			}
			span.End()
		})
	}
}

func classifyRunError(err error) observability.ErrorCategory {
	if err == nil {
		return ""
	}
	if category := observability.ContextErrorCategory(err); category != observability.ErrorCategoryInternal {
		return category
	}
	var executionErr *engine.NodeExecutionError
	if errors.As(err, &executionErr) {
		return observability.ErrorCategoryNodeExecution
	}
	if errors.Is(err, domain.ErrRunEventBudgetExceeded) || errors.Is(err, domain.ErrRunEventSequence) {
		return observability.ErrorCategoryPersistence
	}
	return observability.ErrorCategoryInternal
}

func preparedRunID(prepared *PreparedRun) string {
	if prepared == nil {
		return ""
	}
	return prepared.RunID
}

func normalizedRunMode(prepared *PreparedRun) string {
	if prepared != nil {
		switch prepared.Mode {
		case domain.RunModeTest, domain.RunModePublished, domain.RunModeDebug:
			return string(prepared.Mode)
		}
	}
	return "_OTHER"
}

func normalizedRunStatus(status domain.RunStatus) string {
	switch status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
		return string(status)
	default:
		return "failed"
	}
}

func appendOptionalRunAttribute(attributes []attribute.KeyValue, key, value string) []attribute.KeyValue {
	if value == "" {
		return attributes
	}
	return append(attributes, attribute.String(key, value))
}
