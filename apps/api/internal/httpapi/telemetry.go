package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

var httpDurationBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120,
}

type httpTelemetry struct {
	providers observability.Providers
	tracer    trace.Tracer
	requests  metric.Int64Counter
	duration  metric.Float64Histogram
	active    metric.Int64UpDownCounter
}

func newHTTPTelemetry(providers observability.Providers) *httpTelemetry {
	meter := providers.Meter("agent-studio/httpapi")
	requests, _ := meter.Int64Counter("agent_studio.http.server.requests")
	duration, _ := meter.Float64Histogram(
		"agent_studio.http.server.duration",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(httpDurationBoundaries...),
	)
	active, _ := meter.Int64UpDownCounter("agent_studio.http.server.active_requests")
	return &httpTelemetry{
		providers: providers,
		tracer:    providers.Tracer("agent-studio/httpapi"),
		requests:  requests,
		duration:  duration,
		active:    active,
	}
}

func (telemetry *httpTelemetry) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := telemetry.providers.Propagator().Extract(request.Context(), headerCarrier{request.Header})
		ctx = observability.ContextWithRequestID(ctx, chimiddleware.GetReqID(ctx))
		method := normalizedHTTPMethod(request.Method)
		ctx, span := telemetry.tracer.Start(ctx, "HTTP "+method, trace.WithSpanKind(trace.SpanKindServer))
		request = request.WithContext(ctx)
		wrapped := responseWriter(writer, request)
		methodAttribute := attribute.String("method", method)
		telemetry.active.Add(ctx, 1, metric.WithAttributes(methodAttribute))
		started := time.Now()
		defer func() {
			status := normalizedHTTPStatus(wrapped.Status())
			route := routeTemplate(request)
			statusClass := httpStatusClass(status)
			completedAttributes := []attribute.KeyValue{
				methodAttribute,
				attribute.String("route", route),
				attribute.String("status_class", statusClass),
			}
			telemetry.active.Add(ctx, -1, metric.WithAttributes(methodAttribute))
			telemetry.requests.Add(ctx, 1, metric.WithAttributes(completedAttributes...))
			telemetry.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(completedAttributes...))
			span.SetName(fmt.Sprintf("HTTP %s %s", method, route))
			span.SetAttributes(
				semconv.HTTPRequestMethodKey.String(method),
				semconv.HTTPRoute(route),
				semconv.HTTPResponseStatusCode(status),
			)
			if status >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, "server_error")
			}
			span.End()
		}()
		next.ServeHTTP(wrapped, request)
	})
}

type headerCarrier struct {
	http.Header
}

func (carrier headerCarrier) Get(key string) string {
	return carrier.Header.Get(key)
}

func (carrier headerCarrier) Set(key, value string) {
	carrier.Header.Set(key, value)
}

func (carrier headerCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier.Header))
	for key := range carrier.Header {
		keys = append(keys, key)
	}
	return keys
}

func responseWriter(writer http.ResponseWriter, request *http.Request) chimiddleware.WrapResponseWriter {
	if wrapped, ok := writer.(chimiddleware.WrapResponseWriter); ok {
		return wrapped
	}
	return chimiddleware.NewWrapResponseWriter(writer, request.ProtoMajor)
}

func routeTemplate(request *http.Request) string {
	if routeContext := chi.RouteContext(request.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func normalizedHTTPStatus(status int) int {
	if status == 0 {
		return http.StatusOK
	}
	return status
}

func httpStatusClass(status int) string {
	if status < 200 || status >= 600 {
		return "5xx"
	}
	return fmt.Sprintf("%dxx", status/100)
}

func normalizedHTTPMethod(method string) string {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return method
	default:
		return "_OTHER"
	}
}
