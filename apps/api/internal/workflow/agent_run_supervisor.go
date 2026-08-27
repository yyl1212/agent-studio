package workflow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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

func WithAgentRunSupervisorTelemetry(providers observability.Providers) AgentRunSupervisorOption {
	return func(supervisor *AgentRunSupervisor) {
		supervisor.telemetry = newAgentRunSupervisorTelemetry(providers)
	}
}

type agentRunSupervisorTelemetry struct {
	admissions    metric.Int64Counter
	active        metric.Int64UpDownCounter
	runtimeEvents metric.Int64Counter
}

func newAgentRunSupervisorTelemetry(providers observability.Providers) *agentRunSupervisorTelemetry {
	meter := providers.Meter("agent-studio/workflow")
	admissions, _ := meter.Int64Counter("agent_studio.agent_run.admissions")
	active, _ := meter.Int64UpDownCounter("agent_studio.agent_run.active")
	runtimeEvents, _ := meter.Int64Counter("agent_studio.runtime.events")
	return &agentRunSupervisorTelemetry{admissions: admissions, active: active, runtimeEvents: runtimeEvents}
}

func (telemetry *agentRunSupervisorTelemetry) admission(ctx context.Context, result string) {
	telemetry.admissions.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
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
	telemetry *agentRunSupervisorTelemetry
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
		telemetry: newAgentRunSupervisorTelemetry(observability.Providers{}),
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
		supervisor.telemetry.admission(supervisor.context, "unavailable")
		return nil, ErrAgentRunUnavailable
	}
	select {
	case supervisor.capacity <- struct{}{}:
		supervisor.waitGroup.Add(1)
		supervisor.telemetry.admission(supervisor.context, "accepted")
		supervisor.telemetry.active.Add(supervisor.context, 1)
		return &agentRunReservation{supervisor: supervisor}, nil
	default:
		supervisor.telemetry.admission(supervisor.context, "capacity")
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
	supervisor.telemetry.active.Add(supervisor.context, -1)
	supervisor.waitGroup.Done()
}

type agentRunReservation struct {
	supervisor  *AgentRunSupervisor
	once        sync.Once
	releaseOnce sync.Once
}

func (reservation *agentRunReservation) Launch(requestContext context.Context, prepared *PreparedRun) {
	if requestContext == nil {
		requestContext = context.Background()
	}
	origin := runOrigin{
		spanContext: trace.SpanContextFromContext(requestContext),
		requestID:   observability.RequestIDFromContext(requestContext),
	}
	reservation.once.Do(func() {
		go func() {
			defer func() {
				if recover() != nil {
					runID := ""
					if prepared != nil {
						runID = prepared.RunID
					}
					reservation.supervisor.telemetry.runtimeEvents.Add(reservation.supervisor.context, 1, metric.WithAttributes(
						attribute.String("component", "agent_run"),
						attribute.String("reason", "panic"),
					))
					observability.Log(reservationContext(reservation.supervisor.context, origin), reservation.supervisor.logger, slog.LevelError,
						"asynchronous agent run panicked", observability.IDs{RunID: runID},
						slog.String("error_category", string(observability.ErrorCategoryPanic)),
					)
				}
				reservation.release()
			}()
			_, _ = reservation.supervisor.runner.Execute(reservationContext(reservation.supervisor.context, origin), prepared, nil)
		}()
	})
}

func (reservation *agentRunReservation) Release() {
	reservation.once.Do(reservation.release)
}

func (reservation *agentRunReservation) release() {
	reservation.releaseOnce.Do(reservation.supervisor.release)
}

func reservationContext(supervisorContext context.Context, origin runOrigin) context.Context {
	ctx := observability.ContextWithRequestID(supervisorContext, origin.requestID)
	return contextWithRunOrigin(ctx, origin)
}
