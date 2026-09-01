package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestQueueSamplerSamplesImmediately(t *testing.T) {
	source := newQueueSamplerSource(queueSamplerResult{depth: 3, oldest: 2 * time.Second})
	recorder := newQueueSamplerRecorder()
	sampler := queueSampler{source: source, recorder: recorder}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler.run(ctx, make(chan time.Time))
	}()

	sample := waitForQueueSample(t, recorder.samples)
	if sample.depth != 3 || sample.oldest != 2*time.Second {
		t.Fatalf("sample=(%d, %s)", sample.depth, sample.oldest)
	}
	cancel()
	waitForQueueSamplerExit(t, done)
}

func TestQueueSamplerSamplesOnTick(t *testing.T) {
	source := newQueueSamplerSource(
		queueSamplerResult{depth: 1, oldest: time.Second},
		queueSamplerResult{depth: 2, oldest: 3 * time.Second},
	)
	recorder := newQueueSamplerRecorder()
	ticks := make(chan time.Time)
	sampler := queueSampler{source: source, recorder: recorder}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler.run(ctx, ticks)
	}()

	waitForQueueSample(t, recorder.samples)
	ticks <- time.Time{}
	sample := waitForQueueSample(t, recorder.samples)
	if sample.depth != 2 || sample.oldest != 3*time.Second {
		t.Fatalf("sample=(%d, %s)", sample.depth, sample.oldest)
	}
	cancel()
	waitForQueueSamplerExit(t, done)
}

func TestQueueSamplerContinuesAfterSourceError(t *testing.T) {
	source := newQueueSamplerSource(
		queueSamplerResult{err: errors.New("queue unavailable")},
		queueSamplerResult{depth: 4, oldest: 5 * time.Second},
	)
	recorder := newQueueSamplerRecorder()
	ticks := make(chan time.Time)
	sampler := queueSampler{source: source, recorder: recorder}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler.run(ctx, ticks)
	}()

	waitForQueueSourceCall(t, source.calls)
	select {
	case sample := <-recorder.samples:
		t.Fatalf("recorded source error as sample: %+v", sample)
	default:
	}
	ticks <- time.Time{}
	sample := waitForQueueSample(t, recorder.samples)
	if sample.depth != 4 || sample.oldest != 5*time.Second {
		t.Fatalf("sample=(%d, %s)", sample.depth, sample.oldest)
	}
	cancel()
	waitForQueueSamplerExit(t, done)
}

func TestQueueSamplerRetainsLastSuccessfulMetricsAfterError(t *testing.T) {
	const sentinel = "sentinel-private-queue-error"
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	telemetry := newTelemetry(observability.Providers{MeterProvider: provider})
	source := newQueueSamplerSource(
		queueSamplerResult{depth: 7, oldest: 3 * time.Second},
		queueSamplerResult{err: errors.New(sentinel)},
	)
	sampler := queueSampler{source: source, recorder: telemetryQueueSampleRecorder{telemetry: telemetry}}

	sampler.sample(context.Background())
	sampler.sample(context.Background())

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(collected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), sentinel) {
		t.Fatalf("queue telemetry leaked source error: %s", body)
	}
	metrics := queueSamplerMetrics(collected)
	depth, ok := metrics["agent_studio.worker.queue_depth"].Data.(metricdata.Gauge[int64])
	if !ok || len(depth.DataPoints) != 1 || depth.DataPoints[0].Value != 7 {
		t.Fatalf("queue depth=%#v", metrics["agent_studio.worker.queue_depth"])
	}
	oldest, ok := metrics["agent_studio.worker.oldest_queued_age"].Data.(metricdata.Gauge[float64])
	if !ok || len(oldest.DataPoints) != 1 || oldest.DataPoints[0].Value != 3 {
		t.Fatalf("oldest queued age=%#v", metrics["agent_studio.worker.oldest_queued_age"])
	}
	samples, ok := metrics["agent_studio.worker.queue_sample_total"].Data.(metricdata.Sum[int64])
	if !ok || len(samples.DataPoints) != 2 {
		t.Fatalf("queue samples=%#v", metrics["agent_studio.worker.queue_sample_total"])
	}
	outcomes := make(map[string]int64, len(samples.DataPoints))
	for _, point := range samples.DataPoints {
		for _, attr := range point.Attributes.ToSlice() {
			if attr.Key == "outcome" {
				outcomes[attr.Value.AsString()] = point.Value
			}
		}
	}
	if outcomes["success"] != 1 || outcomes["error"] != 1 || len(outcomes) != 2 {
		t.Fatalf("queue sample outcomes=%#v", outcomes)
	}
}

func TestQueueSamplerStopsAfterCancellation(t *testing.T) {
	source := newQueueSamplerSource(queueSamplerResult{depth: 1})
	recorder := newQueueSamplerRecorder()
	ticks := make(chan time.Time, 1)
	sampler := queueSampler{source: source, recorder: recorder}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler.run(ctx, ticks)
	}()

	waitForQueueSample(t, recorder.samples)
	cancel()
	waitForQueueSamplerExit(t, done)
	callsBeforeTick := source.callCount()
	ticks <- time.Time{}
	if callsAfterTick := source.callCount(); callsAfterTick != callsBeforeTick {
		t.Fatalf("source calls after cancellation=%d, before=%d", callsAfterTick, callsBeforeTick)
	}
}

func TestQueueSamplerDoesNotSampleWhenContextAlreadyCancelled(t *testing.T) {
	source := newQueueSamplerSource(queueSamplerResult{depth: 1})
	sampler := queueSampler{source: source, recorder: newQueueSamplerRecorder()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sampler.run(ctx, make(chan time.Time))

	if calls := source.callCount(); calls != 0 {
		t.Fatalf("source calls with pre-cancelled context=%d", calls)
	}
}

func TestQueueSamplerDoesNotSamplePendingTickAfterCancellation(t *testing.T) {
	source := newQueueSamplerSource(queueSamplerResult{depth: 1})
	recorder := newQueueSamplerRecorder()
	ticks := make(chan time.Time)
	ctx := newCancelAfterTickContext()
	sampler := queueSampler{source: source, recorder: recorder}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampler.run(ctx, ticks)
	}()

	waitForQueueSample(t, recorder.samples)
	tickAccepted := make(chan struct{})
	go func() {
		ticks <- time.Time{}
		ctx.cancelContext()
		close(tickAccepted)
	}()
	waitForQueueSamplerExit(t, done)
	<-tickAccepted
	if calls := source.callCount(); calls != 1 {
		t.Fatalf("source calls after pending tick cancellation=%d", calls)
	}
}

func TestQueueSamplerWithoutSourceReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		(queueSampler{}).Run(context.Background(), 0)
	}()
	waitForQueueSamplerExit(t, done)
}

type queueSamplerResult struct {
	depth  int64
	oldest time.Duration
	err    error
}

type queueSamplerSource struct {
	mu      sync.Mutex
	results []queueSamplerResult
	calls   chan struct{}
}

type cancelAfterTickContext struct {
	context.Context
	mu            sync.Mutex
	errCalls      int
	cancelled     bool
	done          chan struct{}
	cancelledOnce sync.Once
}

func newCancelAfterTickContext() *cancelAfterTickContext {
	return &cancelAfterTickContext{
		Context: context.Background(),
		done:    make(chan struct{}),
	}
}

func (ctx *cancelAfterTickContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *cancelAfterTickContext) Err() error {
	ctx.mu.Lock()
	if ctx.cancelled {
		ctx.mu.Unlock()
		return context.Canceled
	}
	ctx.errCalls++
	errCall := ctx.errCalls
	ctx.mu.Unlock()
	if errCall == 1 {
		return nil
	}
	<-ctx.done
	return context.Canceled
}

func (ctx *cancelAfterTickContext) cancelContext() {
	ctx.cancelledOnce.Do(func() {
		ctx.mu.Lock()
		ctx.cancelled = true
		close(ctx.done)
		ctx.mu.Unlock()
	})
}

func newQueueSamplerSource(results ...queueSamplerResult) *queueSamplerSource {
	return &queueSamplerSource{results: results, calls: make(chan struct{}, len(results)+1)}
}

func (source *queueSamplerSource) RunQueueStats(context.Context) (int64, time.Duration, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls <- struct{}{}
	if len(source.results) == 0 {
		return 0, 0, nil
	}
	result := source.results[0]
	source.results = source.results[1:]
	return result.depth, result.oldest, result.err
}

func (source *queueSamplerSource) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.calls)
}

type recordedQueueSample struct {
	depth  int64
	oldest time.Duration
}

type queueSamplerRecorder struct {
	samples chan recordedQueueSample
}

func newQueueSamplerRecorder() *queueSamplerRecorder {
	return &queueSamplerRecorder{samples: make(chan recordedQueueSample, 4)}
}

func queueSamplerMetrics(collected metricdata.ResourceMetrics) map[string]metricdata.Metrics {
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range collected.ScopeMetrics {
		for _, current := range scope.Metrics {
			metrics[current.Name] = current
		}
	}
	return metrics
}

func (recorder *queueSamplerRecorder) queue(_ context.Context, depth int64, oldest time.Duration) {
	recorder.samples <- recordedQueueSample{depth: depth, oldest: oldest}
}

func (*queueSamplerRecorder) queueSample(context.Context, string) {}

func waitForQueueSourceCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue source call")
	}
}

func waitForQueueSample(t *testing.T, samples <-chan recordedQueueSample) recordedQueueSample {
	t.Helper()
	select {
	case sample := <-samples:
		return sample
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue sample")
		return recordedQueueSample{}
	}
}

func waitForQueueSamplerExit(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queue sampler to stop")
	}
}
