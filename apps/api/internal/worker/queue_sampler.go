package worker

import (
	"context"
	"time"
)

type queueSampleRecorder interface {
	queue(context.Context, int64, time.Duration)
	queueSample(context.Context, string)
}

type queueSampler struct {
	source   queueStatsSource
	recorder queueSampleRecorder
}

type telemetryQueueSampleRecorder struct {
	telemetry *Telemetry
}

func (recorder telemetryQueueSampleRecorder) queue(ctx context.Context, depth int64, oldest time.Duration) {
	recorder.telemetry.queue(ctx, depth, oldest)
}

func (telemetryQueueSampleRecorder) queueSample(context.Context, string) {}

func newQueueSampler(store any, recorder queueSampleRecorder) queueSampler {
	source, _ := store.(queueStatsSource)
	return queueSampler{source: source, recorder: recorder}
}

func (sampler queueSampler) Run(ctx context.Context, interval time.Duration) {
	if sampler.source == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	sampler.run(ctx, ticker.C)
}

func (sampler queueSampler) run(ctx context.Context, ticks <-chan time.Time) {
	sampler.sample(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			sampler.sample(ctx)
		}
	}
}

func (sampler queueSampler) sample(ctx context.Context) {
	depth, oldest, err := sampler.source.RunQueueStats(ctx)
	if err != nil {
		return
	}
	sampler.recorder.queue(ctx, depth, oldest)
}
