package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type nodeTelemetryFixture struct {
	providers    observability.Providers
	spanExporter *tracetest.InMemoryExporter
	metricReader *sdkmetric.ManualReader
	tracer       *sdktrace.TracerProvider
}

func newNodeTelemetryFixture(t *testing.T) nodeTelemetryFixture {
	t.Helper()
	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return nodeTelemetryFixture{
		providers:    observability.Providers{TracerProvider: tracerProvider, MeterProvider: meterProvider},
		spanExporter: spanExporter, metricReader: metricReader, tracer: tracerProvider,
	}
}

type telemetryFixtureNode struct {
	err        error
	panicValue any
}

func (*telemetryFixtureNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{Type: "fixture", Version: "1", ExecutionSafety: agentnode.ExecutionSafetyReadOnly}
}

func (*telemetryFixtureNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	return domain.ResolvedPorts{}, nil
}

func (node *telemetryFixtureNode) Execute(context.Context, domain.NodeRequest) (domain.NodeResult, error) {
	if node.panicValue != nil {
		panic(node.panicValue)
	}
	return domain.NodeResult{}, node.err
}

func TestExecuteNodeTelemetryCreatesChildSpanAndWhitelistedMetrics(t *testing.T) {
	telemetry := newNodeTelemetryFixture(t)
	plan := telemetryNodePlan(&telemetryFixtureNode{})
	ctx, parent := telemetry.tracer.Tracer("test").Start(context.Background(), "workflow.run")
	results := make(chan workerResult, 1)
	executeNode(ctx, plan, "node-secret-id", map[string]any{"sentinel-input": true}, nil, nil, newNodeTelemetry(telemetry.providers), results)
	worker := <-results
	parent.End()
	if worker.err != nil {
		t.Fatal(worker.err)
	}
	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 2 || spans[0].Name != "workflow.node" || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("spans=%#v", spans)
	}
	assertNodeAttributes(t, spans[0].Attributes, map[string]any{
		"agent_studio.node.id":               "node-secret-id",
		"agent_studio.node.type":             "fixture",
		"agent_studio.node.type_version":     "1",
		"agent_studio.node.execution_safety": "read_only",
		"agent_studio.node.status":           "completed",
	})
	metrics := collectNodeMetrics(t, telemetry.metricReader)
	assertNodeCompletionMetrics(t, metrics, "completed", "read_only")
	if nodeTelemetryContains(spans[0], metrics, "sentinel-input") || nodeMetricContains(metrics, "node-secret-id") {
		t.Fatal("node input or ID leaked into metric attributes")
	}
}

func TestExecuteNodeTelemetrySanitizesFailureAndPanic(t *testing.T) {
	tests := []struct {
		name         string
		node         *telemetryFixtureNode
		wantStatus   string
		wantCategory string
	}{
		{name: "failure", node: &telemetryFixtureNode{err: errors.New("sentinel-error-message")}, wantStatus: "failed", wantCategory: "node_execution"},
		{name: "cancelled", node: &telemetryFixtureNode{err: context.Canceled}, wantStatus: "cancelled", wantCategory: "cancelled"},
		{name: "timeout", node: &telemetryFixtureNode{err: context.DeadlineExceeded}, wantStatus: "cancelled", wantCategory: "timeout"},
		{name: "panic", node: &telemetryFixtureNode{panicValue: "sentinel-panic-value"}, wantStatus: "failed", wantCategory: "panic"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			telemetry := newNodeTelemetryFixture(t)
			results := make(chan workerResult, 1)
			executeNode(context.Background(), telemetryNodePlan(test.node), "node", nil, nil, nil, newNodeTelemetry(telemetry.providers), results)
			worker := <-results
			if worker.err == nil {
				t.Fatal("expected node error")
			}
			spans := telemetry.spanExporter.GetSpans()
			if len(spans) != 1 || spans[0].Status.Code != codes.Error || spans[0].Status.Description != test.wantCategory {
				t.Fatalf("spans=%#v", spans)
			}
			metrics := collectNodeMetrics(t, telemetry.metricReader)
			assertNodeFailureMetrics(t, metrics, test.wantStatus, test.wantCategory)
			for _, forbidden := range []string{"sentinel-error-message", "sentinel-panic-value"} {
				if nodeTelemetryContains(spans[0], metrics, forbidden) {
					t.Fatalf("telemetry leaked %q", forbidden)
				}
			}
		})
	}
}

func telemetryNodePlan(executor *telemetryFixtureNode) *Plan {
	return &Plan{Nodes: map[string]CompiledNode{
		"node-secret-id": {
			Node:     domain.Node{ID: "node-secret-id", Type: "fixture", TypeVersion: "1", Config: json.RawMessage(`{"sentinel-config":true}`)},
			Executor: executor, ExecutionSafety: agentnode.ExecutionSafetyReadOnly,
		},
		"node": {
			Node:     domain.Node{ID: "node", Type: "fixture", TypeVersion: "1", Config: json.RawMessage(`{"sentinel-config":true}`)},
			Executor: executor, ExecutionSafety: agentnode.ExecutionSafetyReadOnly,
		},
	}}
}

func collectNodeMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
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

func assertNodeCompletionMetrics(t *testing.T, metrics map[string]metricdata.Metrics, status, safety string) {
	t.Helper()
	want := map[string]any{"node_type": "fixture", "status": status, "execution_safety": safety}
	executions, ok := metrics["agent_studio.workflow.node.executions"].Data.(metricdata.Sum[int64])
	if !ok || len(executions.DataPoints) != 1 || executions.DataPoints[0].Value != 1 {
		t.Fatalf("executions=%#v", metrics["agent_studio.workflow.node.executions"])
	}
	assertNodeAttributes(t, executions.DataPoints[0].Attributes.ToSlice(), want)
	duration, ok := metrics["agent_studio.workflow.node.duration"].Data.(metricdata.Histogram[float64])
	if !ok || len(duration.DataPoints) != 1 || duration.DataPoints[0].Count != 1 {
		t.Fatalf("duration=%#v", metrics["agent_studio.workflow.node.duration"])
	}
	assertNodeAttributes(t, duration.DataPoints[0].Attributes.ToSlice(), want)
}

func assertNodeFailureMetrics(t *testing.T, metrics map[string]metricdata.Metrics, status, category string) {
	t.Helper()
	assertNodeCompletionMetrics(t, metrics, status, "read_only")
	failures, ok := metrics["agent_studio.workflow.node.failures"].Data.(metricdata.Sum[int64])
	if !ok || len(failures.DataPoints) != 1 || failures.DataPoints[0].Value != 1 {
		t.Fatalf("failures=%#v", metrics["agent_studio.workflow.node.failures"])
	}
	assertNodeAttributes(t, failures.DataPoints[0].Attributes.ToSlice(), map[string]any{
		"node_type": "fixture", "execution_safety": "read_only", "error_category": category,
	})
}

func assertNodeAttributes(t *testing.T, attributes []attribute.KeyValue, want map[string]any) {
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

func nodeTelemetryContains(span tracetest.SpanStub, metrics map[string]metricdata.Metrics, forbidden string) bool {
	if bytes.Contains([]byte(span.Name+span.Status.Description), []byte(forbidden)) {
		return true
	}
	for _, current := range span.Attributes {
		if bytes.Contains([]byte(current.Value.Emit()), []byte(forbidden)) {
			return true
		}
	}
	return nodeMetricContains(metrics, forbidden)
}

func nodeMetricContains(metrics map[string]metricdata.Metrics, forbidden string) bool {
	for _, current := range metrics {
		if bytes.Contains([]byte(current.Name+current.Description+current.Unit), []byte(forbidden)) {
			return true
		}
	}
	return false
}
