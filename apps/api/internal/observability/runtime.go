package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	maxResourceAttributeCount  = 32
	maxResourceAttributeLength = 256
	sdkErrorLogInterval        = time.Minute
)

var (
	ErrInitialization = errors.New("observability initialization failed")
	ErrShutdown       = errors.New("observability shutdown failed")
)

type Options struct {
	Endpoint             string
	ServiceName          string
	ServiceVersion       string
	ResourceAttributes   string
	ExportTimeout        time.Duration
	Compression          string
	MetricExportInterval time.Duration
}

type Providers struct {
	TracerProvider    trace.TracerProvider
	MeterProvider     metric.MeterProvider
	TextMapPropagator propagation.TextMapPropagator
}

func (providers Providers) Tracer(name string) trace.Tracer {
	if providers.TracerProvider == nil {
		return tracenoop.NewTracerProvider().Tracer(name)
	}
	return providers.TracerProvider.Tracer(name)
}

func (providers Providers) Meter(name string) metric.Meter {
	if providers.MeterProvider == nil {
		return metricnoop.NewMeterProvider().Meter(name)
	}
	return providers.MeterProvider.Meter(name)
}

func (providers Providers) Propagator() propagation.TextMapPropagator {
	if providers.TextMapPropagator == nil {
		return propagation.TraceContext{}
	}
	return providers.TextMapPropagator
}

type Runtime struct {
	providers            Providers
	meterProvider        *sdkmetric.MeterProvider
	tracerProvider       *sdktrace.TracerProvider
	previousErrorHandler otel.ErrorHandler
	restoreErrorHandler  bool
	shutdownOnce         sync.Once
	shutdownErr          error
}

func NewNoop() *Runtime {
	return &Runtime{providers: Providers{
		TracerProvider:    tracenoop.NewTracerProvider(),
		MeterProvider:     metricnoop.NewMeterProvider(),
		TextMapPropagator: propagation.TraceContext{},
	}}
}

func New(ctx context.Context, options Options, logger *slog.Logger) (*Runtime, error) {
	if options.Endpoint == "" {
		return NewNoop(), nil
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if err := validateOptions(options); err != nil {
		return nil, fmt.Errorf("%w: invalid options", ErrInitialization)
	}

	traceEndpoint, err := signalURL(options.Endpoint, "v1/traces")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid trace endpoint", ErrInitialization)
	}
	metricEndpoint, err := signalURL(options.Endpoint, "v1/metrics")
	if err != nil {
		return nil, fmt.Errorf("%w: invalid metric endpoint", ErrInitialization)
	}
	resourceAttributes, err := parseResourceAttributes(options.ResourceAttributes)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid resource attributes", ErrInitialization)
	}
	resourceAttributes = append(resourceAttributes, semconv.ServiceName(options.ServiceName))
	if options.ServiceVersion != "" {
		resourceAttributes = append(resourceAttributes, semconv.ServiceVersion(options.ServiceVersion))
	}
	resourceValue, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(resourceAttributes...),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: resource", ErrInitialization)
	}

	traceExporter, err := otlptracehttp.New(ctx, traceExporterOptions(options, traceEndpoint)...)
	if err != nil {
		return nil, fmt.Errorf("%w: trace exporter", ErrInitialization)
	}
	metricExporter, err := otlpmetrichttp.New(ctx, metricExporterOptions(options, metricEndpoint)...)
	if err != nil {
		_ = traceExporter.Shutdown(ctx)
		return nil, fmt.Errorf("%w: metric exporter", ErrInitialization)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resourceValue),
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithExportTimeout(options.ExportTimeout),
		),
	)
	reader := sdkmetric.NewPeriodicReader(metricExporter,
		sdkmetric.WithInterval(options.MetricExportInterval),
		sdkmetric.WithTimeout(options.ExportTimeout),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(resourceValue),
		sdkmetric.WithReader(reader),
	)
	previousErrorHandler := otel.GetErrorHandler()
	otel.SetErrorHandler(newRateLimitedErrorHandler(logger))
	return &Runtime{
		providers: Providers{
			TracerProvider:    tracerProvider,
			MeterProvider:     meterProvider,
			TextMapPropagator: propagation.TraceContext{},
		},
		meterProvider:        meterProvider,
		tracerProvider:       tracerProvider,
		previousErrorHandler: previousErrorHandler,
		restoreErrorHandler:  true,
	}, nil
}

func (runtime *Runtime) Providers() Providers {
	if runtime == nil {
		return NewNoop().providers
	}
	return runtime.providers
}

func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.shutdownOnce.Do(func() {
		failed := false
		if runtime.meterProvider != nil {
			if err := runtime.meterProvider.Shutdown(ctx); err != nil {
				failed = true
			}
		}
		if runtime.tracerProvider != nil {
			if err := runtime.tracerProvider.Shutdown(ctx); err != nil {
				failed = true
			}
		}
		if runtime.restoreErrorHandler && runtime.previousErrorHandler != nil {
			otel.SetErrorHandler(runtime.previousErrorHandler)
		}
		if failed {
			runtime.shutdownErr = ErrShutdown
		}
	})
	return runtime.shutdownErr
}

func validateOptions(options Options) error {
	if options.ServiceName == "" || options.ExportTimeout <= 0 || options.MetricExportInterval <= 0 {
		return ErrInitialization
	}
	if options.Compression != "gzip" && options.Compression != "none" {
		return ErrInitialization
	}
	if _, err := signalURL(options.Endpoint, "v1/traces"); err != nil {
		return ErrInitialization
	}
	if _, err := parseResourceAttributes(options.ResourceAttributes); err != nil {
		return ErrInitialization
	}
	return nil
}

func signalURL(endpoint, signalPath string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Hostname() == "" {
		return "", ErrInitialization
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", ErrInitialization
	}
	joined := path.Join(strings.TrimSuffix(parsed.Path, "/"), signalPath)
	parsed.Path = "/" + strings.TrimPrefix(joined, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func parseResourceAttributes(value string) ([]attribute.KeyValue, error) {
	if value == "" {
		return nil, nil
	}
	pairs := strings.Split(value, ",")
	if len(pairs) > maxResourceAttributeCount {
		return nil, ErrInitialization
	}
	attributes := make([]attribute.KeyValue, 0, len(pairs))
	for _, pair := range pairs {
		if len(pair) > maxResourceAttributeLength {
			return nil, ErrInitialization
		}
		key, encodedValue, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return nil, ErrInitialization
		}
		decodedValue, err := url.PathUnescape(strings.TrimSpace(encodedValue))
		if err != nil {
			return nil, ErrInitialization
		}
		attributes = append(attributes, attribute.String(key, decodedValue))
	}
	return attributes, nil
}

func traceExporterOptions(options Options, endpoint string) []otlptracehttp.Option {
	compression := otlptracehttp.NoCompression
	if options.Compression == "gzip" {
		compression = otlptracehttp.GzipCompression
	}
	return []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithCompression(compression),
		otlptracehttp.WithTimeout(options.ExportTimeout),
	}
}

func metricExporterOptions(options Options, endpoint string) []otlpmetrichttp.Option {
	compression := otlpmetrichttp.NoCompression
	if options.Compression == "gzip" {
		compression = otlpmetrichttp.GzipCompression
	}
	return []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithCompression(compression),
		otlpmetrichttp.WithTimeout(options.ExportTimeout),
	}
}

type rateLimitedErrorHandler struct {
	logger *slog.Logger
	mutex  sync.Mutex
	last   time.Time
	now    func() time.Time
}

func newRateLimitedErrorHandler(logger *slog.Logger) *rateLimitedErrorHandler {
	return &rateLimitedErrorHandler{logger: logger, now: time.Now}
}

func (handler *rateLimitedErrorHandler) Handle(error) {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	now := handler.now()
	if !handler.last.IsZero() && now.Sub(handler.last) < sdkErrorLogInterval {
		return
	}
	handler.last = now
	handler.logger.Warn("OpenTelemetry SDK event", "component", "otel_sdk", "reason", "error")
}
