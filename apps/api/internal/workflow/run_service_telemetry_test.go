package workflow

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type runTelemetryTestFixture struct {
	providers    observability.Providers
	spanExporter *tracetest.InMemoryExporter
	metricReader *sdkmetric.ManualReader
	tracer       *sdktrace.TracerProvider
	meter        *sdkmetric.MeterProvider
}

func newRunTelemetryTestFixture(t *testing.T) runTelemetryTestFixture {
	t.Helper()
	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return runTelemetryTestFixture{
		providers:    observability.Providers{TracerProvider: tracerProvider, MeterProvider: meterProvider},
		spanExporter: spanExporter,
		metricReader: metricReader,
		tracer:       tracerProvider,
		meter:        meterProvider,
	}
}

func TestRunTelemetryUsesCallerParentAndFiniteMetricLabels(t *testing.T) {
	telemetry := newRunTelemetryTestFixture(t)
	store := newFakeStore(t)
	const runID = "run-telemetry-success"
	store.runs = append(store.runs, domain.Run{ID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModeTest, Status: domain.RunRunning})
	service := NewRunService(store, nil, &coordinatorAwareEngine{}, WithRunTelemetry(telemetry.providers))
	ctx, parent := telemetry.tracer.Tracer("test").Start(context.Background(), "caller")
	_, err := service.Execute(ctx, &PreparedRun{
		RunID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModeTest,
		Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{}},
	}, nil)
	parent.End()
	if err != nil {
		t.Fatal(err)
	}

	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("span count=%d, want 2", len(spans))
	}
	var runSpan tracetest.SpanStub
	for _, span := range spans {
		if span.Name == "workflow.run" {
			runSpan = span
		}
	}
	if runSpan.Name == "" || runSpan.Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("run span=%#v parent=%s", runSpan, parent.SpanContext().SpanID())
	}
	wantSpanAttributes := map[string]any{
		"agent_studio.run.id":     runID,
		"agent_studio.run.mode":   "test",
		"agent_studio.run.status": "completed",
	}
	assertRunAttributes(t, runSpan.Attributes, wantSpanAttributes)
	if runSpan.Status.Code != codes.Unset {
		t.Fatalf("span status=%#v", runSpan.Status)
	}
	metrics := collectRunMetrics(t, telemetry.metricReader)
	assertRunMetricSet(t, metrics, "test", "completed")
}

func TestRunTelemetrySanitizesNodeFailureAndBalancesActive(t *testing.T) {
	telemetry := newRunTelemetryTestFixture(t)
	store := newFakeStore(t)
	const runID = "run-telemetry-failure"
	store.runs = append(store.runs, domain.Run{ID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModePublished, Status: domain.RunRunning})
	nodeErr := agentnode.NewError(agentnode.ErrorKindInternal, "provider_failed", errors.New("sentinel-cause"), map[string]any{"secret": "sentinel-detail"})
	runErr := &engine.NodeExecutionError{NodeID: "llm-1", NodeType: "llm", Err: nodeErr}
	service := NewRunService(store, nil, failingRunEngine{err: runErr}, WithRunTelemetry(telemetry.providers))

	_, err := service.Execute(context.Background(), &PreparedRun{
		RunID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModePublished,
		Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{}},
	}, nil)
	if !errors.Is(err, nodeErr) {
		t.Fatalf("error=%v", err)
	}

	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error || spans[0].Status.Description != "node_execution" {
		t.Fatalf("spans=%#v", spans)
	}
	assertRunAttributes(t, spans[0].Attributes, map[string]any{
		"agent_studio.run.id":     runID,
		"agent_studio.run.mode":   "published",
		"agent_studio.run.status": "failed",
		"error.category":          "node_execution",
	})
	for _, forbidden := range []string{"sentinel-cause", "sentinel-detail", "provider_failed"} {
		if bytes.Contains([]byte(spans[0].Name+spans[0].Status.Description), []byte(forbidden)) || runSpanContains(spans[0], forbidden) {
			t.Fatalf("span leaked %q", forbidden)
		}
	}
	metrics := collectRunMetrics(t, telemetry.metricReader)
	assertRunMetricSet(t, metrics, "published", "failed")
}

func collectRunMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range collected.ScopeMetrics {
		for _, current := range scope.Metrics {
			metrics[current.Name] = current
		}
	}
	return metrics
}

func assertRunMetricSet(t *testing.T, metrics map[string]metricdata.Metrics, mode, status string) {
	t.Helper()
	if len(metrics) != 3 {
		t.Fatalf("metric count=%d: %#v", len(metrics), metrics)
	}
	wantCompleted := map[string]any{"mode": mode, "status": status}
	runs, ok := metrics["agent_studio.workflow.runs"].Data.(metricdata.Sum[int64])
	if !ok || len(runs.DataPoints) != 1 || runs.DataPoints[0].Value != 1 {
		t.Fatalf("runs=%#v", metrics["agent_studio.workflow.runs"])
	}
	assertRunAttributes(t, runs.DataPoints[0].Attributes.ToSlice(), wantCompleted)
	duration, ok := metrics["agent_studio.workflow.run.duration"].Data.(metricdata.Histogram[float64])
	if !ok || len(duration.DataPoints) != 1 || duration.DataPoints[0].Count != 1 {
		t.Fatalf("duration=%#v", metrics["agent_studio.workflow.run.duration"])
	}
	assertRunAttributes(t, duration.DataPoints[0].Attributes.ToSlice(), wantCompleted)
	active, ok := metrics["agent_studio.workflow.run.active"].Data.(metricdata.Sum[int64])
	if !ok || len(active.DataPoints) != 1 || active.DataPoints[0].Value != 0 {
		t.Fatalf("active=%#v", metrics["agent_studio.workflow.run.active"])
	}
	assertRunAttributes(t, active.DataPoints[0].Attributes.ToSlice(), map[string]any{"mode": mode})
}

func assertRunAttributes(t *testing.T, attributes []attribute.KeyValue, want map[string]any) {
	t.Helper()
	got := make(map[string]any, len(attributes))
	for _, current := range attributes {
		got[string(current.Key)] = current.Value.AsInterface()
	}
	if len(got) != len(want) {
		t.Fatalf("attributes=%#v, want=%#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("attribute %s=%v, want=%v; all=%#v", key, got[key], value, got)
		}
	}
}

func runSpanContains(span tracetest.SpanStub, value string) bool {
	for _, current := range span.Attributes {
		if current.Value.Emit() == value {
			return true
		}
	}
	return false
}
