package workflow

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
)

const (
	coordinatorHeartbeatInterval = time.Second
	coordinatorSweepInterval     = 10 * time.Second
	coordinatorStaleAfter        = 15 * time.Second
	coordinatorBatchSize         = 500
)

type CoordinatorTicker interface {
	C() <-chan time.Time
	Stop()
}

type CoordinatorClock interface {
	NewTicker(time.Duration) CoordinatorTicker
}

type systemCoordinatorClock struct{}

func (systemCoordinatorClock) NewTicker(interval time.Duration) CoordinatorTicker {
	return systemCoordinatorTicker{Ticker: time.NewTicker(interval)}
}

type systemCoordinatorTicker struct {
	*time.Ticker
}

func (ticker systemCoordinatorTicker) C() <-chan time.Time { return ticker.Ticker.C }

type RunCoordinatorOption func(*RunCoordinator)

func WithCoordinatorClock(clock CoordinatorClock) RunCoordinatorOption {
	return func(coordinator *RunCoordinator) {
		if clock != nil {
			coordinator.clock = clock
		}
	}
}

func WithCoordinatorLogger(logger *slog.Logger) RunCoordinatorOption {
	return func(coordinator *RunCoordinator) {
		if logger != nil {
			coordinator.logger = logger
		}
	}
}

type RunCoordinator struct {
	store  RunCoordinationStore
	clock  CoordinatorClock
	logger *slog.Logger

	mutex   sync.Mutex
	active  map[string]context.CancelFunc
	closing bool
}

func NewRunCoordinator(store RunCoordinationStore, options ...RunCoordinatorOption) *RunCoordinator {
	coordinator := &RunCoordinator{
		store: store, clock: systemCoordinatorClock{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		active: make(map[string]context.CancelFunc),
	}
	for _, option := range options {
		option(coordinator)
	}
	return coordinator
}

func (coordinator *RunCoordinator) Register(parent context.Context, runID string) (context.Context, func()) {
	runContext, cancel := context.WithCancel(parent)
	coordinator.mutex.Lock()
	if coordinator.closing {
		coordinator.mutex.Unlock()
		cancel()
		return runContext, func() {}
	}
	if _, exists := coordinator.active[runID]; exists {
		coordinator.mutex.Unlock()
		cancel()
		return runContext, func() {}
	}
	coordinator.active[runID] = cancel
	coordinator.mutex.Unlock()
	var once sync.Once
	return runContext, func() {
		once.Do(func() {
			coordinator.mutex.Lock()
			delete(coordinator.active, runID)
			coordinator.mutex.Unlock()
			cancel()
		})
	}
}

func (coordinator *RunCoordinator) CancelLocal(runID string) bool {
	coordinator.mutex.Lock()
	cancel, exists := coordinator.active[runID]
	coordinator.mutex.Unlock()
	if exists {
		cancel()
	}
	return exists
}

func (coordinator *RunCoordinator) BeginShutdown() {
	coordinator.mutex.Lock()
	if coordinator.closing {
		coordinator.mutex.Unlock()
		return
	}
	coordinator.closing = true
	cancels := make([]context.CancelFunc, 0, len(coordinator.active))
	for _, cancel := range coordinator.active {
		cancels = append(cancels, cancel)
	}
	coordinator.active = make(map[string]context.CancelFunc)
	coordinator.mutex.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (coordinator *RunCoordinator) Run(ctx context.Context) error {
	heartbeats := coordinator.clock.NewTicker(coordinatorHeartbeatInterval)
	sweeps := coordinator.clock.NewTicker(coordinatorSweepInterval)
	defer heartbeats.Stop()
	defer sweeps.Stop()
	defer coordinator.BeginShutdown()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeats.C():
			if err := coordinator.heartbeat(ctx); err != nil {
				observability.Log(ctx, coordinator.logger, slog.LevelError, "run heartbeat failed", observability.IDs{},
					slog.String("error_category", string(observability.ErrorCategoryPersistence)),
				)
			}
		case <-sweeps.C():
			if _, err := coordinator.store.FinalizeInterruptedRuns(ctx, int(coordinatorStaleAfter/time.Second), coordinatorBatchSize); err != nil {
				observability.Log(ctx, coordinator.logger, slog.LevelError, "interrupted run sweep failed", observability.IDs{},
					slog.String("error_category", string(observability.ErrorCategoryPersistence)),
				)
			}
		}
	}
}

func (coordinator *RunCoordinator) heartbeat(ctx context.Context) error {
	coordinator.mutex.Lock()
	ids := make([]string, 0, len(coordinator.active))
	for id := range coordinator.active {
		ids = append(ids, id)
	}
	coordinator.mutex.Unlock()
	if len(ids) == 0 {
		return nil
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += coordinatorBatchSize {
		end := min(start+coordinatorBatchSize, len(ids))
		cancelled, err := coordinator.store.HeartbeatRuns(ctx, ids[start:end])
		if err != nil {
			return err
		}
		for _, id := range cancelled {
			coordinator.CancelLocal(id)
		}
	}
	return nil
}
