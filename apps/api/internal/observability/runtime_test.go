package observability

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

type capturedOTLPRequest struct {
	path            string
	contentEncoding string
	body            []byte
}

func clearExporterEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_HEADERS",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS",
		"OTEL_EXPORTER_OTLP_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE",
		"OTEL_EXPORTER_OTLP_COMPRESSION",
		"OTEL_EXPORTER_OTLP_TRACES_COMPRESSION",
		"OTEL_EXPORTER_OTLP_METRICS_COMPRESSION",
		"OTEL_EXPORTER_OTLP_TIMEOUT",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT",
		"OTEL_EXPORTER_OTLP_METRICS_TIMEOUT",
		"OTEL_SERVICE_NAME",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(key, "")
	}
}

func TestSignalURLPreservesBasePath(t *testing.T) {
	got, err := signalURL("https://collector.example/tenant/otel/", "v1/traces")
	if err != nil {
		t.Fatalf("signalURL() error = %v", err)
	}
	if want := "https://collector.example/tenant/otel/v1/traces"; got != want {
		t.Fatalf("signalURL() = %q, want %q", got, want)
	}
}

func TestNewNoopProvidesSafeProviders(t *testing.T) {
	runtime, err := New(context.Background(), Options{Endpoint: ""}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	providers := runtime.Providers()
	ctx, span := providers.Tracer("test").Start(context.Background(), "noop")
	span.End()
	if span.SpanContext().IsValid() {
		t.Fatal("noop span must be invalid")
	}
	counter, err := providers.Meter("test").Int64Counter("test.counter")
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}
	counter.Add(ctx, 1)
	if got := providers.Propagator().Fields(); !slices.Equal(got, []string{"traceparent", "tracestate"}) {
		t.Fatalf("propagator fields = %v", got)
	}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

func TestNewExportsTraceAndMetricToExplicitSignalPaths(t *testing.T) {
	clearExporterEnv(t)
	var mutex sync.Mutex
	requests := make([]capturedOTLPRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := io.Reader(request.Body)
		if request.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(request.Body)
			if err != nil {
				t.Errorf("gzip.NewReader() error = %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			defer reader.Close()
			body = reader
		}
		encoded, err := io.ReadAll(body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mutex.Lock()
		requests = append(requests, capturedOTLPRequest{
			path:            request.URL.Path,
			contentEncoding: request.Header.Get("Content-Encoding"),
			body:            encoded,
		})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/x-protobuf")
		switch request.URL.Path {
		case "/tenant/otel/v1/traces":
			response, _ := proto.Marshal(&collectortracepb.ExportTraceServiceResponse{})
			_, _ = writer.Write(response)
		case "/tenant/otel/v1/metrics":
			response, _ := proto.Marshal(&collectormetricpb.ExportMetricsServiceResponse{})
			_, _ = writer.Write(response)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runtime, err := New(context.Background(), Options{
		Endpoint:             server.URL + "/tenant/otel",
		ServiceName:          "agent-studio-runtime-test",
		ServiceVersion:       "v-test",
		ResourceAttributes:   "deployment.environment=unit%20test",
		ExportTimeout:        time.Second,
		Compression:          "gzip",
		MetricExportInterval: time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, span := runtime.Providers().Tracer("runtime-test").Start(context.Background(), "runtime-span")
	counter, err := runtime.Providers().Meter("runtime-test").Int64Counter("runtime.counter")
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}
	counter.Add(ctx, 1)
	span.End()

	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var waitGroup sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			errorsSeen <- runtime.Shutdown(shutdownContext)
		}()
	}
	waitGroup.Wait()
	close(errorsSeen)
	for shutdownErr := range errorsSeen {
		if shutdownErr != nil {
			t.Fatalf("Shutdown() error = %v", shutdownErr)
		}
	}

	mutex.Lock()
	captured := append([]capturedOTLPRequest(nil), requests...)
	mutex.Unlock()
	if len(captured) != 2 {
		t.Fatalf("captured %d OTLP requests, want 2: %#v", len(captured), captured)
	}
	slices.SortFunc(captured, func(left, right capturedOTLPRequest) int {
		return strings.Compare(left.path, right.path)
	})
	if captured[0].path != "/tenant/otel/v1/metrics" || captured[1].path != "/tenant/otel/v1/traces" {
		t.Fatalf("unexpected signal paths: %#v", captured)
	}
	for _, request := range captured {
		if request.contentEncoding != "gzip" || len(request.body) == 0 {
			t.Fatalf("request %s encoding=%q bytes=%d", request.path, request.contentEncoding, len(request.body))
		}
		assertOTLPResource(t, request)
	}
}

func TestNewRejectsInvalidOptionsWithoutDisclosingValues(t *testing.T) {
	clearExporterEnv(t)
	tests := []Options{
		{
			Endpoint:             "https://secret-user@collector.example/otel",
			ServiceName:          "service",
			ExportTimeout:        time.Second,
			Compression:          "gzip",
			MetricExportInterval: time.Second,
		},
		{
			Endpoint:             "https://collector.example/otel",
			ServiceName:          "service",
			ResourceAttributes:   "secret-missing-value",
			ExportTimeout:        time.Second,
			Compression:          "gzip",
			MetricExportInterval: time.Second,
		},
		{
			Endpoint:             "https://collector.example/otel",
			ServiceName:          "service",
			ExportTimeout:        time.Second,
			Compression:          "secret-compression",
			MetricExportInterval: time.Second,
		},
		{
			Endpoint:             "https://collector.example/otel",
			ServiceName:          "service",
			ExportTimeout:        0,
			Compression:          "gzip",
			MetricExportInterval: time.Second,
		},
	}
	for index, options := range tests {
		_, err := New(context.Background(), options, nil)
		if !errors.Is(err, ErrInitialization) {
			t.Fatalf("case %d New() error = %v, want ErrInitialization", index, err)
		}
		if strings.Contains(err.Error(), "secret-") || strings.Contains(err.Error(), options.Endpoint) {
			t.Fatalf("case %d error disclosed configuration: %v", index, err)
		}
	}
}

func TestRuntimeShutdownSanitizesExporterFailure(t *testing.T) {
	clearExporterEnv(t)
	const sentinel = "secret-export-response"
	var logOutput strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(sentinel))
	}))
	defer server.Close()

	runtime, err := New(context.Background(), Options{
		Endpoint:             server.URL,
		ServiceName:          "agent-studio-runtime-test",
		ServiceVersion:       "v-test",
		ExportTimeout:        250 * time.Millisecond,
		Compression:          "none",
		MetricExportInterval: time.Hour,
	}, slog.New(slog.NewJSONHandler(&logOutput, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, span := runtime.Providers().Tracer("runtime-test").Start(context.Background(), "failed-span")
	span.End()
	counter, err := runtime.Providers().Meter("runtime-test").Int64Counter("failed.counter")
	if err != nil {
		t.Fatalf("Int64Counter() error = %v", err)
	}
	counter.Add(context.Background(), 1)

	shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownErr := runtime.Shutdown(shutdownContext)
	if !errors.Is(shutdownErr, ErrShutdown) {
		t.Fatalf("Shutdown() error = %v, want ErrShutdown", shutdownErr)
	}
	if strings.Contains(shutdownErr.Error(), sentinel) || strings.Contains(logOutput.String(), sentinel) {
		t.Fatalf("exporter response leaked: error=%v log=%s", shutdownErr, logOutput.String())
	}
	if secondErr := runtime.Shutdown(shutdownContext); !errors.Is(secondErr, ErrShutdown) {
		t.Fatalf("second Shutdown() error = %v", secondErr)
	}
}

func TestRuntimeRateLimitsSDKErrorsAndRestoresPreviousHandler(t *testing.T) {
	clearExporterEnv(t)
	var previousCalls atomic.Int64
	original := otel.GetErrorHandler()
	previous := otel.ErrorHandlerFunc(func(error) { previousCalls.Add(1) })
	otel.SetErrorHandler(previous)
	t.Cleanup(func() { otel.SetErrorHandler(original) })

	var logOutput strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	runtime, err := New(context.Background(), Options{
		Endpoint:             server.URL,
		ServiceName:          "agent-studio-runtime-test",
		ServiceVersion:       "v-test",
		ExportTimeout:        time.Second,
		Compression:          "none",
		MetricExportInterval: time.Hour,
	}, slog.New(slog.NewJSONHandler(&logOutput, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	otel.Handle(errors.New("secret-sdk-error-one"))
	otel.Handle(errors.New("secret-sdk-error-two"))
	if strings.Count(logOutput.String(), "OpenTelemetry SDK event") != 1 {
		t.Fatalf("SDK errors were not rate limited: %s", logOutput.String())
	}
	if strings.Contains(logOutput.String(), "secret-sdk-error") {
		t.Fatalf("SDK error leaked: %s", logOutput.String())
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	otel.Handle(errors.New("after-shutdown"))
	if previousCalls.Load() != 1 {
		t.Fatalf("previous handler calls = %d, want 1", previousCalls.Load())
	}
}

func assertOTLPResource(t *testing.T, request capturedOTLPRequest) {
	t.Helper()
	var resource *resourcepb.Resource
	switch request.path {
	case "/tenant/otel/v1/traces":
		var payload collectortracepb.ExportTraceServiceRequest
		if err := proto.Unmarshal(request.body, &payload); err != nil {
			t.Fatalf("unmarshal trace request: %v", err)
		}
		if len(payload.ResourceSpans) != 1 || len(payload.ResourceSpans[0].ScopeSpans) != 1 || len(payload.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
			t.Fatalf("unexpected trace payload: %#v", payload.ResourceSpans)
		}
		if payload.ResourceSpans[0].ScopeSpans[0].Spans[0].Name != "runtime-span" {
			t.Fatalf("unexpected span name: %q", payload.ResourceSpans[0].ScopeSpans[0].Spans[0].Name)
		}
		resource = payload.ResourceSpans[0].Resource
	case "/tenant/otel/v1/metrics":
		var payload collectormetricpb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(request.body, &payload); err != nil {
			t.Fatalf("unmarshal metric request: %v", err)
		}
		if len(payload.ResourceMetrics) != 1 || len(payload.ResourceMetrics[0].ScopeMetrics) != 1 || len(payload.ResourceMetrics[0].ScopeMetrics[0].Metrics) != 1 {
			t.Fatalf("unexpected metric payload: %#v", payload.ResourceMetrics)
		}
		if payload.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name != "runtime.counter" {
			t.Fatalf("unexpected metric name: %q", payload.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name)
		}
		resource = payload.ResourceMetrics[0].Resource
	default:
		t.Fatalf("unexpected OTLP path %q", request.path)
	}
	attributes := stringAttributes(resource.Attributes)
	if attributes["service.name"] != "agent-studio-runtime-test" {
		t.Fatalf("service.name = %q", attributes["service.name"])
	}
	if attributes["service.version"] != "v-test" {
		t.Fatalf("service.version = %q", attributes["service.version"])
	}
	if attributes["deployment.environment"] != "unit test" {
		t.Fatalf("deployment.environment = %q", attributes["deployment.environment"])
	}
}

func stringAttributes(attributes []*commonpb.KeyValue) map[string]string {
	values := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		if value, ok := attribute.Value.Value.(*commonpb.AnyValue_StringValue); ok {
			values[attribute.Key] = value.StringValue
		}
	}
	return values
}
