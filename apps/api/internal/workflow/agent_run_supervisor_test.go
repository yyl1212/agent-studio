package workflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

type supervisorExecutor struct {
	started chan context.Context
	release chan struct{}
	panic   bool
	once    sync.Once
}

func (executor *supervisorExecutor) Execute(ctx context.Context, prepared *PreparedRun, _ engine.Observer) (engine.RunResult, error) {
	executor.once.Do(func() { executor.started <- ctx })
	if executor.panic {
		panic("executor panic")
	}
	if executor.release != nil {
		select {
		case <-executor.release:
			return engine.RunResult{RunID: prepared.RunID}, nil
		case <-ctx.Done():
			return engine.RunResult{RunID: prepared.RunID}, ctx.Err()
		}
	}
	return engine.RunResult{RunID: prepared.RunID}, nil
}

func TestAgentRunSupervisorRejectsReservationAtCapacity(t *testing.T) {
	supervisor := NewAgentRunSupervisor(context.Background(), 1, &supervisorExecutor{started: make(chan context.Context, 1)})
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Reserve(); !errors.Is(err, ErrAgentRunCapacity) {
		t.Fatalf("second reserve error=%v", err)
	}
	reservation.Release()
	if err := supervisor.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunSupervisorReleaseReturnsCapacity(t *testing.T) {
	supervisor := NewAgentRunSupervisor(context.Background(), 1, &supervisorExecutor{started: make(chan context.Context, 1)})
	first, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	second, err := supervisor.Reserve()
	if err != nil {
		t.Fatalf("reserve after release: %v", err)
	}
	second.Release()
	if err := supervisor.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunSupervisorLaunchUsesSupervisorContext(t *testing.T) {
	type contextKey struct{}
	parent := context.WithValue(context.Background(), contextKey{}, "supervisor")
	executor := &supervisorExecutor{started: make(chan context.Context, 1), release: make(chan struct{})}
	supervisor := NewAgentRunSupervisor(parent, 1, executor)
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(&PreparedRun{RunID: "run-context"})
	runContext := <-executor.started
	if runContext.Value(contextKey{}) != "supervisor" {
		t.Fatalf("context value=%v", runContext.Value(contextKey{}))
	}
	close(executor.release)
	if err := supervisor.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunSupervisorShutdownCancelsRunsAndWaits(t *testing.T) {
	executor := &supervisorExecutor{started: make(chan context.Context, 1), release: make(chan struct{})}
	supervisor := NewAgentRunSupervisor(context.Background(), 1, executor)
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(&PreparedRun{RunID: "run-shutdown"})
	runContext := <-executor.started
	supervisor.BeginShutdown()
	if _, err := supervisor.Reserve(); !errors.Is(err, ErrAgentRunUnavailable) {
		t.Fatalf("reserve during shutdown error=%v", err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(runContext.Err(), context.Canceled) {
		t.Fatalf("run context error=%v", runContext.Err())
	}
}

func TestAgentRunSupervisorReleaseUnblocksShutdownWait(t *testing.T) {
	supervisor := NewAgentRunSupervisor(context.Background(), 1, &supervisorExecutor{started: make(chan context.Context, 1)})
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	supervisor.BeginShutdown()
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Wait(waitContext) }()
	select {
	case err := <-done:
		t.Fatalf("wait returned before release: %v", err)
	default:
	}
	reservation.Release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunSupervisorRecoversExecutorPanicAndReturnsCapacity(t *testing.T) {
	executor := &supervisorExecutor{started: make(chan context.Context, 1), panic: true}
	supervisor := NewAgentRunSupervisor(context.Background(), 1, executor)
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(&PreparedRun{RunID: "run-panic"})
	<-executor.started
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := supervisor.Wait(waitContext); err != nil {
		t.Fatal(err)
	}
	if len(supervisor.capacity) != 0 {
		t.Fatalf("capacity still occupied=%d", len(supervisor.capacity))
	}
}
