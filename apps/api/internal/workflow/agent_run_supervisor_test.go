package workflow

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type supervisorExecutor struct {
	started    chan context.Context
	release    chan struct{}
	panic      bool
	panicValue string
	once       sync.Once
}

func (executor *supervisorExecutor) Execute(ctx context.Context, prepared *PreparedRun, _ engine.Observer) (engine.RunResult, error) {
	executor.once.Do(func() { executor.started <- ctx })
	if executor.panic {
		panic(executor.panicValue)
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
	reservation.Launch(context.Background(), &PreparedRun{RunID: "run-context"})
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
	reservation.Launch(context.Background(), &PreparedRun{RunID: "run-shutdown"})
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
	executor := &supervisorExecutor{started: make(chan context.Context, 1), panic: true, panicValue: "executor panic"}
	supervisor := NewAgentRunSupervisor(context.Background(), 1, executor)
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(context.Background(), &PreparedRun{RunID: "run-panic"})
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

type asyncTelemetryContextKey struct{}

type gatedRunExecutor struct {
	delegate  AgentRunExecutor
	started   chan context.Context
	release   chan struct{}
	completed chan struct{}
}

func (executor *gatedRunExecutor) Execute(ctx context.Context, prepared *PreparedRun, observer engine.Observer) (engine.RunResult, error) {
	executor.started <- ctx
	select {
	case <-executor.release:
		result, err := executor.delegate.Execute(ctx, prepared, observer)
		close(executor.completed)
		return result, err
	case <-ctx.Done():
		return engine.RunResult{RunID: prepared.RunID}, ctx.Err()
	}
}

func TestAgentRunSupervisorTelemetryCreatesLinkedRootAndIsolatesRequestContext(t *testing.T) {
	telemetry := newRunTelemetryTestFixture(t)
	store := newFakeStore(t)
	const runID = "run-async-telemetry"
	store.runs = append(store.runs, domain.Run{ID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModePublished, Status: domain.RunRunning})
	runService := NewRunService(store, nil, &coordinatorAwareEngine{}, WithRunTelemetry(telemetry.providers))
	executor := &gatedRunExecutor{delegate: runService, started: make(chan context.Context, 1), release: make(chan struct{}), completed: make(chan struct{})}
	supervisor := NewAgentRunSupervisor(context.Background(), 1, executor, WithAgentRunSupervisorTelemetry(telemetry.providers))
	requestContext, cancelRequest := context.WithCancel(context.WithValue(context.Background(), asyncTelemetryContextKey{}, "sentinel-business-value"))
	requestContext = observability.ContextWithRequestID(requestContext, "request-async-1")
	requestContext, requestSpan := telemetry.tracer.Tracer("test").Start(requestContext, "HTTP POST /agent")
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(requestContext, &PreparedRun{
		RunID: runID, WorkflowID: store.workflow.ID, Mode: domain.RunModePublished,
		Plan: &engine.Plan{Nodes: map[string]engine.CompiledNode{}},
	})
	runContext := <-executor.started
	if runContext.Value(asyncTelemetryContextKey{}) != nil || observability.RequestIDFromContext(runContext) != "request-async-1" {
		t.Fatalf("isolated context business=%v requestId=%q", runContext.Value(asyncTelemetryContextKey{}), observability.RequestIDFromContext(runContext))
	}
	cancelRequest()
	select {
	case <-runContext.Done():
		t.Fatalf("request cancellation reached asynchronous run: %v", runContext.Err())
	default:
	}
	close(executor.release)
	<-executor.completed
	if err := supervisor.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	requestSpan.End()

	spans := telemetry.spanExporter.GetSpans()
	var runSpan tracetest.SpanStub
	for index := range spans {
		if spans[index].Name == "workflow.run" {
			runSpan = spans[index]
		}
	}
	if runSpan.Name == "" || runSpan.Parent.IsValid() || len(runSpan.Links) != 1 || runSpan.Links[0].SpanContext.SpanID() != requestSpan.SpanContext().SpanID() {
		t.Fatalf("asynchronous run span=%#v", runSpan)
	}
}

func TestAgentRunSupervisorTelemetryBalancesAdmissionAndActiveMetrics(t *testing.T) {
	telemetry := newRunTelemetryTestFixture(t)
	supervisor := NewAgentRunSupervisor(context.Background(), 1, &supervisorExecutor{started: make(chan context.Context, 1)}, WithAgentRunSupervisorTelemetry(telemetry.providers))
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Reserve(); !errors.Is(err, ErrAgentRunCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	supervisor.BeginShutdown()
	if _, err := supervisor.Reserve(); !errors.Is(err, ErrAgentRunUnavailable) {
		t.Fatalf("unavailable error=%v", err)
	}
	reservation.Release()
	metrics := collectRunMetrics(t, telemetry.metricReader)
	assertAdmissionResults(t, metrics["agent_studio.agent_run.admissions"], map[string]int64{"accepted": 1, "capacity": 1, "unavailable": 1})
	active, ok := metrics["agent_studio.agent_run.active"].Data.(metricdata.Sum[int64])
	if !ok || len(active.DataPoints) != 1 || active.DataPoints[0].Value != 0 || active.DataPoints[0].Attributes.Len() != 0 {
		t.Fatalf("active=%#v", metrics["agent_studio.agent_run.active"])
	}
}

func TestAgentRunSupervisorTelemetrySanitizesPanicAndBalancesActive(t *testing.T) {
	telemetry := newRunTelemetryTestFixture(t)
	var logs bytes.Buffer
	executor := &supervisorExecutor{started: make(chan context.Context, 1), panic: true, panicValue: "sentinel-panic-value"}
	supervisor := NewAgentRunSupervisor(context.Background(), 1, executor,
		WithAgentRunSupervisorTelemetry(telemetry.providers),
		WithAgentRunSupervisorLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)
	reservation, err := supervisor.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	reservation.Launch(observability.ContextWithRequestID(context.Background(), "panic-request"), &PreparedRun{RunID: "run-panic-telemetry"})
	<-executor.started
	if err := supervisor.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(logs.Bytes(), []byte("sentinel-panic-value")) || !bytes.Contains(logs.Bytes(), []byte(`"error_category":"panic"`)) || !bytes.Contains(logs.Bytes(), []byte(`"requestId":"panic-request"`)) {
		t.Fatalf("unsafe panic log=%s", logs.String())
	}
	metrics := collectRunMetrics(t, telemetry.metricReader)
	active, ok := metrics["agent_studio.agent_run.active"].Data.(metricdata.Sum[int64])
	if !ok || len(active.DataPoints) != 1 || active.DataPoints[0].Value != 0 {
		t.Fatalf("active=%#v", metrics["agent_studio.agent_run.active"])
	}
	runtimeEvents, ok := metrics["agent_studio.runtime.events"].Data.(metricdata.Sum[int64])
	if !ok || len(runtimeEvents.DataPoints) != 1 || runtimeEvents.DataPoints[0].Value != 1 {
		t.Fatalf("runtime events=%#v", metrics["agent_studio.runtime.events"])
	}
	assertRunAttributes(t, runtimeEvents.DataPoints[0].Attributes.ToSlice(), map[string]any{"component": "agent_run", "reason": "panic"})
}

func assertAdmissionResults(t *testing.T, metricValue metricdata.Metrics, want map[string]int64) {
	t.Helper()
	admissions, ok := metricValue.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("admissions=%#v", metricValue)
	}
	got := make(map[string]int64, len(admissions.DataPoints))
	for _, point := range admissions.DataPoints {
		attrs := point.Attributes.ToSlice()
		if len(attrs) != 1 || string(attrs[0].Key) != "result" {
			t.Fatalf("admission attrs=%#v", attrs)
		}
		got[attrs[0].Value.AsString()] = point.Value
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("admission results=%v, want=%v", got, want)
	}
}
