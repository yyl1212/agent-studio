package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type httpTelemetryTestFixture struct {
	providers    observability.Providers
	spanExporter *tracetest.InMemoryExporter
	metricReader *sdkmetric.ManualReader
}

func newHTTPTelemetryTestFixture(t *testing.T) httpTelemetryTestFixture {
	t.Helper()
	spanExporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(context.Background())
		_ = tracerProvider.Shutdown(context.Background())
	})
	return httpTelemetryTestFixture{
		providers: observability.Providers{
			TracerProvider:    tracerProvider,
			MeterProvider:     meterProvider,
			TextMapPropagator: propagation.TraceContext{},
		},
		spanExporter: spanExporter,
		metricReader: metricReader,
	}
}

func TestHTTPTelemetryUsesRouteTemplateAndRemoteParent(t *testing.T) {
	telemetry := newHTTPTelemetryTestFixture(t)
	var logs bytes.Buffer
	dependencies := fixtureDeps()
	dependencies.Telemetry = telemetry.providers
	dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	request := httptest.NewRequest(http.MethodGet, "/api/workflows/secret-workflow?sentinel=query", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()

	NewRouter(dependencies).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	service := dependencies.Workflows.(*fixtureWorkflowService)
	if service.lastRequestID == "" {
		t.Fatal("request ID was not copied into the generic observability context")
	}
	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name != "HTTP GET /api/workflows/{id}" || span.SpanKind != trace.SpanKindServer {
		t.Fatalf("span name=%q kind=%v", span.Name, span.SpanKind)
	}
	if span.Parent.SpanID().String() != "00f067aa0ba902b7" || !span.Parent.IsRemote() {
		t.Fatalf("parent = %s remote=%v", span.Parent.SpanID(), span.Parent.IsRemote())
	}
	attributes := spanAttributeMap(span.Attributes)
	wantSpanAttributes := map[string]any{
		"http.request.method":       "GET",
		"http.route":                "/api/workflows/{id}",
		"http.response.status_code": int64(http.StatusOK),
	}
	assertExactAttributes(t, attributes, wantSpanAttributes)
	assertSafeHTTPLogs(t, logs.Bytes(), "/api/workflows/{id}", http.StatusOK)
	metrics := collectHTTPMetrics(t, telemetry.metricReader)
	assertHTTPMetricSet(t, metrics, "GET", "/api/workflows/{id}", "2xx")
	for _, forbidden := range []string{"secret-workflow", "sentinel", "query"} {
		if bytes.Contains(logs.Bytes(), []byte(forbidden)) || metricsContain(metrics, forbidden) || spanContains(span, forbidden) {
			t.Fatalf("telemetry leaked %q", forbidden)
		}
	}
}

func TestHTTPTelemetryUsesUnmatchedForUnknownRoutes(t *testing.T) {
	telemetry := newHTTPTelemetryTestFixture(t)
	var logs bytes.Buffer
	dependencies := fixtureDeps()
	dependencies.Telemetry = telemetry.providers
	dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	request := httptest.NewRequest(http.MethodGet, "/missing/secret-path?sentinel=query", nil)
	response := httptest.NewRecorder()

	NewRouter(dependencies).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "HTTP GET unmatched" {
		t.Fatalf("spans = %#v", spans)
	}
	if spans[0].Status.Code != codes.Unset {
		t.Fatalf("404 span status = %v", spans[0].Status)
	}
	assertSafeHTTPLogs(t, logs.Bytes(), "unmatched", http.StatusNotFound)
	metrics := collectHTTPMetrics(t, telemetry.metricReader)
	assertHTTPMetricSet(t, metrics, "GET", "unmatched", "4xx")
	for _, forbidden := range []string{"secret-path", "sentinel", "/missing/"} {
		if bytes.Contains(logs.Bytes(), []byte(forbidden)) || metricsContain(metrics, forbidden) || spanContains(spans[0], forbidden) {
			t.Fatalf("unmatched telemetry leaked %q", forbidden)
		}
	}
}

func TestHTTPTelemetryBalancesPanicAndSanitizesLogs(t *testing.T) {
	telemetry := newHTTPTelemetryTestFixture(t)
	var logs bytes.Buffer
	dependencies := fixtureDeps()
	dependencies.Telemetry = telemetry.providers
	dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	dependencies.Workflows.(*fixtureWorkflowService).panicOnList = true
	response := performRequest(NewRouter(dependencies), http.MethodGet, "/api/workflows", "")

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "HTTP GET /api/workflows" {
		t.Fatalf("spans = %#v", spans)
	}
	if spans[0].Status.Code != codes.Error || spans[0].Status.Description != "server_error" {
		t.Fatalf("panic span status = %#v", spans[0].Status)
	}
	if bytes.Contains(logs.Bytes(), []byte("test panic")) {
		t.Fatalf("panic value leaked: %s", logs.String())
	}
	lines := decodeHTTPLogLines(t, logs.Bytes())
	if len(lines) != 2 {
		t.Fatalf("log line count = %d: %s", len(lines), logs.String())
	}
	var sawPanic, sawAccess bool
	for _, line := range lines {
		switch line["msg"] {
		case "HTTP panic recovered":
			sawPanic = line["error_category"] == "panic" && line["traceId"] != nil && line["requestId"] != nil
		case "HTTP request":
			sawAccess = line["route"] == "/api/workflows" && line["status"] == float64(http.StatusInternalServerError)
		}
	}
	if !sawPanic || !sawAccess {
		t.Fatalf("panic/access logs missing safe fields: %#v", lines)
	}
	metrics := collectHTTPMetrics(t, telemetry.metricReader)
	assertHTTPMetricSet(t, metrics, "GET", "/api/workflows", "5xx")
}

func TestHTTPTelemetryMarksPanicAfterResponseStarted(t *testing.T) {
	telemetry := newHTTPTelemetryTestFixture(t)
	var logs bytes.Buffer
	dependencies := fixtureDeps()
	dependencies.Telemetry = telemetry.providers
	dependencies.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	handler := &handler{dependencies: dependencies}
	inner := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		panic("secret-panic-after-write")
	})
	chain := chimiddleware.RequestID(newHTTPTelemetry(dependencies.Telemetry).middleware(
		handler.recoverMiddleware(handler.accessLogMiddleware(inner)),
	))
	response := httptest.NewRecorder()

	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/secret-after-write", nil))

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d", response.Code)
	}
	spans := telemetry.spanExporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Error || spans[0].Status.Description != "panic" {
		t.Fatalf("panic span status = %#v", spans)
	}
	if bytes.Contains(logs.Bytes(), []byte("secret-panic-after-write")) {
		t.Fatalf("panic value leaked: %s", logs.String())
	}
	lines := decodeHTTPLogLines(t, logs.Bytes())
	var accessStatus float64
	for _, line := range lines {
		if line["msg"] == "HTTP request" {
			accessStatus, _ = line["status"].(float64)
		}
	}
	if accessStatus != float64(http.StatusAccepted) {
		t.Fatalf("access status = %v, logs=%#v", accessStatus, lines)
	}
	metrics := collectHTTPMetrics(t, telemetry.metricReader)
	assertHTTPMetricSet(t, metrics, "GET", "unmatched", "2xx")
}

func collectHTTPMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range collected.ScopeMetrics {
		for _, current := range scope.Metrics {
			metrics[current.Name] = current
		}
	}
	return metrics
}

func assertHTTPMetricSet(t *testing.T, metrics map[string]metricdata.Metrics, method, route, statusClass string) {
	t.Helper()
	if len(metrics) != 3 {
		t.Fatalf("metric count = %d: %#v", len(metrics), metrics)
	}
	wantCompleted := map[string]any{"method": method, "route": route, "status_class": statusClass}
	requests, ok := metrics["agent_studio.http.server.requests"].Data.(metricdata.Sum[int64])
	if !ok || len(requests.DataPoints) != 1 || requests.DataPoints[0].Value != 1 {
		t.Fatalf("requests metric = %#v", metrics["agent_studio.http.server.requests"])
	}
	assertExactAttributes(t, attributeSetMap(requests.DataPoints[0].Attributes), wantCompleted)
	duration, ok := metrics["agent_studio.http.server.duration"].Data.(metricdata.Histogram[float64])
	if !ok || len(duration.DataPoints) != 1 || duration.DataPoints[0].Count != 1 {
		t.Fatalf("duration metric = %#v", metrics["agent_studio.http.server.duration"])
	}
	assertExactAttributes(t, attributeSetMap(duration.DataPoints[0].Attributes), wantCompleted)
	active, ok := metrics["agent_studio.http.server.active_requests"].Data.(metricdata.Sum[int64])
	if !ok || len(active.DataPoints) != 1 || active.DataPoints[0].Value != 0 {
		t.Fatalf("active metric = %#v", metrics["agent_studio.http.server.active_requests"])
	}
	assertExactAttributes(t, attributeSetMap(active.DataPoints[0].Attributes), map[string]any{"method": method})
}

func assertSafeHTTPLogs(t *testing.T, encoded []byte, route string, status int) {
	t.Helper()
	lines := decodeHTTPLogLines(t, encoded)
	if len(lines) != 1 {
		t.Fatalf("log line count = %d: %s", len(lines), encoded)
	}
	line := lines[0]
	if line["msg"] != "HTTP request" || line["method"] != "GET" || line["route"] != route || line["status"] != float64(status) {
		t.Fatalf("access log = %#v", line)
	}
	if line["requestId"] == nil || line["traceId"] == nil || line["spanId"] == nil {
		t.Fatalf("access log lacks correlation fields: %#v", line)
	}
	if _, exists := line["path"]; exists {
		t.Fatalf("raw path field exists: %#v", line)
	}
}

func decodeHTTPLogLines(t *testing.T, encoded []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	lines := make([]map[string]any, 0, 2)
	for decoder.More() {
		var line map[string]any
		if err := decoder.Decode(&line); err != nil {
			t.Fatalf("decode logs: %v", err)
		}
		lines = append(lines, line)
	}
	return lines
}

func spanAttributeMap(attributes []attribute.KeyValue) map[string]any {
	values := make(map[string]any, len(attributes))
	for _, current := range attributes {
		values[string(current.Key)] = current.Value.AsInterface()
	}
	return values
}

func attributeSetMap(attributes attribute.Set) map[string]any {
	return spanAttributeMap(attributes.ToSlice())
}

func assertExactAttributes(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("attribute count = %d, want %d: %#v", len(got), len(want), got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("attribute %s = %#v, want %#v; all=%#v", key, got[key], value, got)
		}
	}
}

func metricsContain(metrics map[string]metricdata.Metrics, forbidden string) bool {
	encoded, err := json.Marshal(metrics)
	return err == nil && strings.Contains(string(encoded), forbidden)
}

func spanContains(span tracetest.SpanStub, forbidden string) bool {
	encoded, err := json.Marshal(spanAttributeMap(span.Attributes))
	return err == nil && strings.Contains(string(encoded), forbidden)
}
