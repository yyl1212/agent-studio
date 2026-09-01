package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

func TestCheckpointValidateRejectsInvalidState(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	valid := Checkpoint{
		LastSequence: 4,
		RunStarted:   true,
		NodeStatuses: map[string]domain.NodeStatus{"start": domain.NodeCompleted},
		NodeAttempts: map[string]int{"start": 1},
		FrozenEdges:  map[string]FrozenEdge{"e1": {Active: true, Value: "yes"}},
	}
	if err := valid.Validate(plan); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Checkpoint)
	}{
		{"negative sequence", func(value *Checkpoint) { value.LastSequence = -1 }},
		{"started without sequence", func(value *Checkpoint) { value.LastSequence = 0 }},
		{"history before started", func(value *Checkpoint) { value.RunStarted = false }},
		{"unknown node", func(value *Checkpoint) { value.NodeAttempts["missing"] = 1 }},
		{"running status", func(value *Checkpoint) { value.NodeStatuses["start"] = domain.NodeRunning }},
		{"failed status", func(value *Checkpoint) { value.NodeStatuses["start"] = domain.NodeFailed }},
		{"missing attempt", func(value *Checkpoint) { delete(value.NodeAttempts, "start") }},
		{"invalid attempt", func(value *Checkpoint) { value.NodeAttempts["start"] = 4 }},
		{"missing frozen edge", func(value *Checkpoint) { delete(value.FrozenEdges, "e1") }},
		{"unknown frozen edge", func(value *Checkpoint) { value.FrozenEdges["missing"] = FrozenEdge{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			checkpoint := cloneCheckpoint(valid)
			test.mutate(&checkpoint)
			if err := checkpoint.Validate(plan); err == nil {
				t.Fatal("Validate error = nil")
			}
		})
	}
}

func TestEngineRunFromCheckpointContinuesSequenceAttemptsAndFrozenEdges(t *testing.T) {
	plan, branch := compileConditionalRuntimeFixture(t, nil)
	observer := &memoryObserver{}
	checkpoint := Checkpoint{
		LastSequence: 9,
		RunStarted:   true,
		NodeStatuses: map[string]domain.NodeStatus{
			"start": domain.NodeCompleted, "condition": domain.NodeCompleted, "false-node": domain.NodeSkipped,
		},
		NodeAttempts: map[string]int{"start": 1, "condition": 1, "false-node": 1, "true-node": 1},
		FrozenEdges: map[string]FrozenEdge{
			"e1": {Active: true, Value: "yes"}, "e2": {Active: true, Value: "yes"},
			"e3": {Active: false}, "e5": {Active: false},
		},
	}
	result, err := New(Options{}).RunFromCheckpoint(context.Background(), "resume-1", plan, map[string]any{"value": "yes"}, observer, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "true-result" || !branch.completed("true-result") {
		t.Fatalf("result=%+v", result)
	}
	events := observer.Events()
	if len(events) == 0 || events[0].Sequence != 10 || events[len(events)-1].Type != "run.completed" {
		t.Fatalf("events=%+v", events)
	}
	for _, event := range events {
		if event.NodeID == "start" || event.NodeID == "condition" || event.NodeID == "false-node" {
			t.Fatalf("terminal node emitted again: %+v", event)
		}
		if event.NodeID == "true-node" && event.NodeAttempt != 2 {
			t.Fatalf("true-node attempt=%d", event.NodeAttempt)
		}
		if event.NodeID == "end" && event.NodeAttempt != 1 {
			t.Fatalf("end attempt=%d", event.NodeAttempt)
		}
	}
}

func TestEngineRunFromQueuedCheckpointEmitsRunStartedAfterQueuedSequence(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	observer := &memoryObserver{}
	_, err := New(Options{}).RunFromCheckpoint(context.Background(), "queued-1", plan, map[string]any{"value": "yes"}, observer, Checkpoint{LastSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	events := observer.Events()
	if len(events) == 0 || events[0].Type != "run.started" || events[0].Sequence != 2 {
		t.Fatalf("events=%+v", events)
	}
}

func TestEngineNewRunStartsAtSequenceOneAndAttemptOne(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	observer := &memoryObserver{}
	if _, err := New(Options{}).Run(context.Background(), "new-1", plan, map[string]any{"value": "yes"}, observer); err != nil {
		t.Fatal(err)
	}
	events := observer.Events()
	if len(events) == 0 || events[0].Type != "run.started" || events[0].Sequence != 1 {
		t.Fatalf("events=%+v", events)
	}
	for _, event := range events {
		if event.NodeID != "" && event.NodeAttempt != 1 {
			t.Fatalf("event=%+v", event)
		}
	}
}

func TestEngineRunFromCheckpointRejectsAttemptLimit(t *testing.T) {
	plan, _ := compileConditionalRuntimeFixture(t, nil)
	checkpoint := Checkpoint{LastSequence: 2, RunStarted: true, NodeAttempts: map[string]int{"start": 3}}
	_, err := New(Options{}).RunFromCheckpoint(context.Background(), "attempt-limit", plan, map[string]any{"value": "yes"}, &memoryObserver{}, checkpoint)
	if err == nil || !strings.Contains(err.Error(), "attempt") {
		t.Fatalf("err=%v", err)
	}
}
