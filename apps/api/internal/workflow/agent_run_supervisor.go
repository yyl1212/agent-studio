package workflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
)

var (
	ErrAgentRunCapacity    = errors.New("agent run capacity reached")
	ErrAgentRunUnavailable = errors.New("agent run supervisor unavailable")
)

type AgentRunExecutor interface {
	Execute(context.Context, *PreparedRun, engine.Observer) (engine.RunResult, error)
}

type AgentRunSupervisorOption func(*AgentRunSupervisor)

func WithAgentRunSupervisorLogger(logger *slog.Logger) AgentRunSupervisorOption {
	return func(supervisor *AgentRunSupervisor) {
		if logger != nil {
			supervisor.logger = logger
		}
	}
}

type AgentRunSupervisor struct {
	context   context.Context
	cancel    context.CancelFunc
	runner    AgentRunExecutor
	logger    *slog.Logger
	capacity  chan struct{}
	mutex     sync.Mutex
	accepting bool
	waitGroup sync.WaitGroup
}

func NewAgentRunSupervisor(parent context.Context, maxActive int, runner AgentRunExecutor, options ...AgentRunSupervisorOption) *AgentRunSupervisor {
	if parent == nil {
		parent = context.Background()
	}
	if maxActive < 1 {
		maxActive = 1
	}
	runContext, cancel := context.WithCancel(parent)
	supervisor := &AgentRunSupervisor{
		context: runContext, cancel: cancel, runner: runner,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		capacity: make(chan struct{}, maxActive), accepting: true,
	}
	for _, option := range options {
		option(supervisor)
	}
	return supervisor
}

func (supervisor *AgentRunSupervisor) Reserve() (AgentRunReservation, error) {
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	if !supervisor.accepting {
		return nil, ErrAgentRunUnavailable
	}
	select {
	case supervisor.capacity <- struct{}{}:
		supervisor.waitGroup.Add(1)
		return &agentRunReservation{supervisor: supervisor}, nil
	default:
		return nil, ErrAgentRunCapacity
	}
}

func (supervisor *AgentRunSupervisor) BeginShutdown() {
	supervisor.mutex.Lock()
	defer supervisor.mutex.Unlock()
	if !supervisor.accepting {
		return
	}
	supervisor.accepting = false
	supervisor.cancel()
}

func (supervisor *AgentRunSupervisor) Wait(ctx context.Context) error {
	supervisor.BeginShutdown()
	done := make(chan struct{})
	go func() {
		supervisor.waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (supervisor *AgentRunSupervisor) release() {
	<-supervisor.capacity
	supervisor.waitGroup.Done()
}

type agentRunReservation struct {
	supervisor *AgentRunSupervisor
	once       sync.Once
}

func (reservation *agentRunReservation) Launch(prepared *PreparedRun) {
	reservation.once.Do(func() {
		go func() {
			defer reservation.supervisor.release()
			defer func() {
				if recovered := recover(); recovered != nil {
					runID := ""
					if prepared != nil {
						runID = prepared.RunID
					}
					reservation.supervisor.logger.Error("asynchronous agent run panicked", "run_id", runID, "panic", recovered)
				}
			}()
			_, _ = reservation.supervisor.runner.Execute(reservation.supervisor.context, prepared, nil)
		}()
	})
}

func (reservation *agentRunReservation) Release() {
	reservation.once.Do(reservation.supervisor.release)
}
