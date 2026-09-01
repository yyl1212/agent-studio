package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
