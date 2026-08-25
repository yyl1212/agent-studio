package workflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

type fakeCoordinationStore struct {
	mutex             sync.Mutex
	heartbeatBatches  [][]string
	cancelled         map[string]bool
	finalizeCalls     [][2]int
	finalizeCompleted chan struct{}
	heartbeatErr      error
}

func (store *fakeCoordinationStore) HeartbeatRuns(_ context.Context, ids []string) ([]string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.heartbeatBatches = append(store.heartbeatBatches, append([]string{}, ids...))
	if store.heartbeatErr != nil {
		return nil, store.heartbeatErr
	}
	cancelled := make([]string, 0)
	for _, id := range ids {
		if store.cancelled[id] {
			cancelled = append(cancelled, id)
		}
	}
	return cancelled, nil
}

func (store *fakeCoordinationStore) FinalizeInterruptedRuns(_ context.Context, staleAfterSeconds, limit int) (int, error) {
	store.mutex.Lock()
	store.finalizeCalls = append(store.finalizeCalls, [2]int{staleAfterSeconds, limit})
	store.mutex.Unlock()
	if store.finalizeCompleted != nil {
		store.finalizeCompleted <- struct{}{}
	}
	return 0, nil
}

type manualTicker struct {
	ticks chan time.Time
}

func (ticker *manualTicker) C() <-chan time.Time { return ticker.ticks }
func (ticker *manualTicker) Stop()               {}

type manualCoordinatorClock struct {
	mutex   sync.Mutex
	tickers map[time.Duration]*manualTicker
	ready   map[time.Duration]chan struct{}
}

func newManualCoordinatorClock() *manualCoordinatorClock {
	return &manualCoordinatorClock{
		tickers: make(map[time.Duration]*manualTicker),
		ready: map[time.Duration]chan struct{}{
			coordinatorHeartbeatInterval: make(chan struct{}),
			coordinatorSweepInterval:     make(chan struct{}),
		},
	}
}

func (clock *manualCoordinatorClock) NewTicker(interval time.Duration) CoordinatorTicker {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	ticker := &manualTicker{ticks: make(chan time.Time)}
	clock.tickers[interval] = ticker
	close(clock.ready[interval])
	return ticker
}

func (clock *manualCoordinatorClock) tick(interval time.Duration) {
	<-clock.ready[interval]
	clock.mutex.Lock()
	ticker := clock.tickers[interval]
	clock.mutex.Unlock()
	ticker.ticks <- time.Time{}
}

func TestRunCoordinatorSkipsEmptyHeartbeatAndBatchesStableIDs(t *testing.T) {
	store := &fakeCoordinationStore{}
	coordinator := NewRunCoordinator(store)
	if err := coordinator.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.heartbeatBatches) != 0 {
		t.Fatalf("empty active set called store: %+v", store.heartbeatBatches)
	}
	releases := make([]func(), 0, 501)
	for index := 500; index >= 0; index-- {
		_, release := coordinator.Register(context.Background(), runCoordinatorID(index))
		releases = append(releases, release)
	}
	t.Cleanup(func() {
		for _, release := range releases {
			release()
		}
	})
	if err := coordinator.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.heartbeatBatches) != 2 || len(store.heartbeatBatches[0]) != 500 || len(store.heartbeatBatches[1]) != 1 {
		t.Fatalf("heartbeat batches=%v", batchLengths(store.heartbeatBatches))
	}
	want := append([]string{}, store.heartbeatBatches[0]...)
	want = append(want, store.heartbeatBatches[1]...)
	sorted := append([]string{}, want...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(want, sorted) {
		t.Fatalf("heartbeat IDs are not stable and sorted")
	}
}

func TestRunCoordinatorCancelsRemoteRequestsAndStopsAfterUnregister(t *testing.T) {
	store := &fakeCoordinationStore{cancelled: map[string]bool{"run-cancel": true}}
	coordinator := NewRunCoordinator(store)
	runContext, release := coordinator.Register(context.Background(), "run-cancel")
	if err := coordinator.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runContext.Done():
	default:
		t.Fatal("remote cancellation did not cancel local context")
	}
	release()
	store.heartbeatBatches = nil
	if err := coordinator.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.heartbeatBatches) != 0 {
		t.Fatalf("unregistered run remained active: %+v", store.heartbeatBatches)
	}
}

func TestRunCoordinatorBeginShutdownRejectsNewRegistrations(t *testing.T) {
	store := &fakeCoordinationStore{}
	coordinator := NewRunCoordinator(store)
	existing, release := coordinator.Register(context.Background(), "existing")
	defer release()
	coordinator.BeginShutdown()
	select {
	case <-existing.Done():
	default:
		t.Fatal("shutdown did not cancel existing registration")
	}
	late, _ := coordinator.Register(context.Background(), "late")
	select {
	case <-late.Done():
	default:
		t.Fatal("shutdown accepted a late healthy registration")
	}
	if err := coordinator.heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.heartbeatBatches) != 0 {
		t.Fatalf("shutdown coordinator heartbeated registrations: %+v", store.heartbeatBatches)
	}
}

func TestRunCoordinatorHeartbeatFailureKeepsHealthyContext(t *testing.T) {
	wantErr := errors.New("database unavailable")
	store := &fakeCoordinationStore{heartbeatErr: wantErr}
	coordinator := NewRunCoordinator(store)
	runContext, release := coordinator.Register(context.Background(), "healthy")
	defer release()
	if err := coordinator.heartbeat(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("heartbeat error=%v", err)
	}
	select {
	case <-runContext.Done():
		t.Fatal("heartbeat failure cancelled a healthy context")
	default:
	}
}

func TestRunCoordinatorLoopUsesManualTicksAndCancelsAllOnExit(t *testing.T) {
	clock := newManualCoordinatorClock()
	store := &fakeCoordinationStore{cancelled: map[string]bool{"run-cancel": true}, finalizeCompleted: make(chan struct{}, 1)}
	coordinator := NewRunCoordinator(store, WithCoordinatorClock(clock), WithCoordinatorLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	cancelContext, cancelRelease := coordinator.Register(context.Background(), "run-cancel")
	defer cancelRelease()
	otherContext, otherRelease := coordinator.Register(context.Background(), "run-other")
	defer otherRelease()
	runContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(runContext) }()
	clock.tick(coordinatorHeartbeatInterval)
	select {
	case <-cancelContext.Done():
	case <-time.After(time.Second):
		t.Fatal("heartbeat tick was not processed")
	}
	clock.tick(coordinatorSweepInterval)
	select {
	case <-store.finalizeCompleted:
	case <-time.After(time.Second):
		t.Fatal("sweep tick was not processed")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-otherContext.Done():
	default:
		t.Fatal("coordinator exit did not cancel remaining local context")
	}
	if len(store.finalizeCalls) != 1 || store.finalizeCalls[0] != [2]int{15, 500} {
		t.Fatalf("finalize calls=%v", store.finalizeCalls)
	}
}

func runCoordinatorID(index int) string {
	return "run-" + time.Unix(int64(index), 0).UTC().Format("150405.000000000")
}

func batchLengths(batches [][]string) []int {
	lengths := make([]int, len(batches))
	for index := range batches {
		lengths[index] = len(batches[index])
	}
	return lengths
}
