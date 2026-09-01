package workflow

import (
	"context"
	"errors"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

type runEventSource interface {
	ListRunEvents(context.Context, string, int64, int) ([]domain.RunEvent, error)
}

type RunEventFollower struct {
	store        runEventSource
	pollInterval time.Duration
}

func NewRunEventFollower(store runEventSource, pollInterval time.Duration) *RunEventFollower {
	return &RunEventFollower{store: store, pollInterval: pollInterval}
}

func (follower *RunEventFollower) Follow(ctx context.Context, runID string, observer engine.Observer) error {
	if follower == nil || follower.store == nil || follower.pollInterval <= 0 || runID == "" || observer == nil {
		return errors.New("run event follower dependencies are incomplete")
	}
	afterSequence := int64(0)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		events, err := follower.store.ListRunEvents(ctx, runID, afterSequence, 200)
		if err != nil {
			return err
		}
		for _, event := range events {
			if event.Sequence != afterSequence+1 {
				return domain.ErrRunEventSequence
			}
			if err := observer.Observe(ctx, runEventToEngineEvent(event)); err != nil {
				return err
			}
			afterSequence = event.Sequence
			if isRunTerminal(event.Type) {
				return nil
			}
		}
		timer.Reset(follower.pollInterval)
	}
}
