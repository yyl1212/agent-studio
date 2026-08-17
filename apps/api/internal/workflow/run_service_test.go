package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentstudio.local/api/internal/domain"
	"agentstudio.local/api/internal/engine"
	"agentstudio.local/api/internal/nodes/builtin"
)

type recordingObserver struct {
	events []engine.Event
	err    error
}

func (observer *recordingObserver) Observe(_ context.Context, event engine.Event) error {
	if observer.err != nil {
		return observer.err
	}
	observer.events = append(observer.events, event)
	return nil
}

func TestPrepareAgentUsesRequestedVersionAfterNewPublish(t *testing.T) {
	service, store := newRunServiceFixture(t)
	v1Graph := graphReturning(t, "v1")
	v1Schema, err := inputSchemaForGraph(v1Graph)
	if err != nil {
		t.Fatal(err)
	}
	v1 := store.AddVersion(v1Graph, v1Schema)
	v2Graph := graphReturning(t, "v2")
	v2Schema, err := inputSchemaForGraph(v2Graph)
	if err != nil {
		t.Fatal(err)
	}
	store.SetCurrentVersion(store.AddVersion(v2Graph, v2Schema))

	prepared, err := service.PrepareAgent(context.Background(), "demo", v1.ID, map[string]any{"topic": ""})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), prepared, &recordingObserver{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "v1" {
		t.Fatalf("output=%v", result.Output)
	}
}

func TestTestRunStoresGraphSnapshotBeforeExecution(t *testing.T) {
	service, store := newRunServiceFixture(t)
	workflow := store.workflow
	prepared, err := service.PrepareDraft(context.Background(), workflow.ID, workflow.DraftRevision, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.LastRun().GraphSnapshot, workflow.DraftGraph) {
		t.Fatal("snapshot not persisted during prepare")
	}
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareRejectsUnknownInputAndExecutePropagatesObservers(t *testing.T) {
	service, store := newRunServiceFixture(t)
	versionGraph := graphReturning(t, "v1")
	schema, err := inputSchemaForGraph(versionGraph)
	if err != nil {
		t.Fatal(err)
	}
	version := store.AddVersion(versionGraph, schema)
	store.SetCurrentVersion(version)
	if _, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"unknown": true}); !errors.Is(err, ErrInputValidation) {
		t.Fatalf("input error=%v", err)
	}

	prepared, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	observerError := errors.New("stream failed")
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{err: observerError}); !errors.Is(err, observerError) {
		t.Fatalf("observer error=%v", err)
	}

	prepared, err = service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"topic": "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	store.failUpsert = errors.New("persistence failed")
	if _, err := service.Execute(context.Background(), prepared, &recordingObserver{}); !errors.Is(err, store.failUpsert) {
		t.Fatalf("persistence error=%v", err)
	}
}

func TestPreparePersistsRedactedInputAndCancellationFinishesRun(t *testing.T) {
	service, store := newRunServiceFixture(t)
	graph := graphReturningField(t, "", "token")
	schema, err := inputSchemaForGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	version := store.AddVersion(graph, schema)
	store.SetCurrentVersion(version)
	prepared, err := service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"token": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var persistedInput map[string]any
	if err := json.Unmarshal(store.LastRun().Input, &persistedInput); err != nil {
		t.Fatal(err)
	}
	if persistedInput["token"] != "[REDACTED]" {
		t.Fatalf("persisted input=%v", persistedInput)
	}
	recorder := &recordingObserver{}
	if _, err := service.Execute(context.Background(), prepared, recorder); err != nil {
		t.Fatal(err)
	}
	var downstreamInput map[string]any
	for _, event := range recorder.events {
		if event.Type == "node_started" && event.NodeID == "start" {
			downstreamInput = event.Input.(map[string]any)
		}
	}
	var storedNodeInput map[string]any
	if err := json.Unmarshal(store.nodeRuns["start"].Input, &storedNodeInput); err != nil {
		t.Fatal(err)
	}
	if downstreamInput["token"] != "[REDACTED]" || storedNodeInput["token"] != "[REDACTED]" {
		t.Fatalf("downstream=%v stored=%v", downstreamInput, storedNodeInput)
	}

	prepared, err = service.PrepareAgent(context.Background(), "demo", version.ID, map[string]any{"token": "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.Execute(ctx, prepared, &recordingObserver{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if store.LastRun().Status != domain.RunCancelled {
		t.Fatalf("run status=%s", store.LastRun().Status)
	}
}

func newRunServiceFixture(t *testing.T) (*RunService, *fakeStore) {
	t.Helper()
	store := newFakeStore(t)
	return NewRunService(store, newRealCompiler(t), engine.New(engine.Options{})), store
}

func inputSchemaForGraph(raw json.RawMessage) (json.RawMessage, error) {
	var graph struct {
		Nodes []struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &graph); err != nil {
		return nil, err
	}
	for _, node := range graph.Nodes {
		if node.Type == "start" {
			return builtin.DeriveInputSchema(node.Config)
		}
	}
	return nil, errors.New("start missing")
}
