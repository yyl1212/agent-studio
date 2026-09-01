package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestRunEventFollowerPollsExclusivelyUntilTerminal(t *testing.T) {
	store := &followerStore{pages: [][]domain.RunEvent{
		{{RunID: "run", Sequence: 1, Type: "run.queued"}},
		{{RunID: "run", Sequence: 2, Type: "run.started"}, {RunID: "run", Sequence: 3, Type: "run.completed"}},
	}}
	observer := &recordingObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := NewRunEventFollower(store, time.Millisecond).Follow(ctx, "run", observer); err != nil {
		t.Fatal(err)
	}
	if len(observer.events) != 3 || len(store.cursors) != 2 || store.cursors[0] != 0 || store.cursors[1] != 1 {
		t.Fatalf("events=%+v cursors=%v", observer.events, store.cursors)
	}
}

func TestRunEventFollowerDisconnectDoesNotMutateRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &followerStore{}
	err := NewRunEventFollower(store, time.Millisecond).Follow(ctx, "run", &recordingObserver{})
	if !errors.Is(err, context.Canceled) || store.mutations != 0 {
		t.Fatalf("error=%v mutations=%d", err, store.mutations)
	}
}

func TestRunEventFollowerRejectsSequenceGap(t *testing.T) {
	store := &followerStore{pages: [][]domain.RunEvent{{
		{RunID: "run", Sequence: 1, Type: "run.queued"},
		{RunID: "run", Sequence: 3, Type: "run.completed"},
	}}}
	err := NewRunEventFollower(store, time.Millisecond).Follow(context.Background(), "run", &recordingObserver{})
	if !errors.Is(err, domain.ErrRunEventSequence) {
		t.Fatalf("error=%v", err)
	}
}

func TestRunEventFollowerStopsAfterRecoveryRequired(t *testing.T) {
	store := &followerStore{pages: [][]domain.RunEvent{{
		{RunID: "run", Sequence: 1, Type: "run.queued"},
		{RunID: "run", Sequence: 2, Type: "run.recovery_required"},
	}}}
	observer := &recordingObserver{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := NewRunEventFollower(store, time.Millisecond).Follow(ctx, "run", observer); err != nil {
		t.Fatal(err)
	}
	if len(observer.events) != 2 || len(store.cursors) != 1 {
		t.Fatalf("events=%+v cursors=%v", observer.events, store.cursors)
	}
}

type followerStore struct {
	pages     [][]domain.RunEvent
	cursors   []int64
	mutations int
}

func (store *followerStore) ListRunEvents(_ context.Context, _ string, after int64, _ int) ([]domain.RunEvent, error) {
	store.cursors = append(store.cursors, after)
	if len(store.pages) == 0 {
		return []domain.RunEvent{}, nil
	}
	page := store.pages[0]
	store.pages = store.pages[1:]
	return page, nil
}
