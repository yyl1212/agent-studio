package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
)

type schedulerFixtureNode struct {
	result       domain.NodeResult
	resolveCalls int
}

func (*schedulerFixtureNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{Type: "fixture", Version: "1"}
}

func (node *schedulerFixtureNode) Resolve(json.RawMessage) (domain.ResolvedPorts, error) {
	node.resolveCalls++
	return domain.ResolvedPorts{Outputs: []domain.PortDefinition{{Key: "drifted"}}}, nil
}

func (node *schedulerFixtureNode) Execute(context.Context, domain.NodeRequest) (domain.NodeResult, error) {
	return node.result, nil
}

func TestExecuteNodeValidatesAgainstCompiledPortsWithoutResolvingAgain(t *testing.T) {
	executor := &schedulerFixtureNode{result: domain.NodeResult{Outputs: map[string]any{"compiled": "ok"}}}
	plan := &Plan{Nodes: map[string]CompiledNode{
		"node": {
			Node:     domain.Node{Config: json.RawMessage(`{}`)},
			Executor: executor,
			Ports:    domain.ResolvedPorts{Outputs: []domain.PortDefinition{{Key: "compiled"}}},
		},
	}}
	results := make(chan workerResult, 1)
	executeNode(context.Background(), plan, "node", nil, nil, nil, results)
	worker := <-results
	if worker.err != nil {
		t.Fatal(worker.err)
	}
	if executor.resolveCalls != 0 {
		t.Fatalf("Resolve calls = %d, want 0", executor.resolveCalls)
	}
}

func TestExecuteNodeRejectsOutputsOutsideCompiledPorts(t *testing.T) {
	executor := &schedulerFixtureNode{result: domain.NodeResult{Outputs: map[string]any{"unknown": true}}}
	plan := &Plan{Nodes: map[string]CompiledNode{
		"node": {
			Node:     domain.Node{Config: json.RawMessage(`{}`)},
			Executor: executor,
			Ports:    domain.ResolvedPorts{Outputs: []domain.PortDefinition{{Key: "compiled"}}},
		},
	}}
	results := make(chan workerResult, 1)
	executeNode(context.Background(), plan, "node", nil, nil, nil, results)
	worker := <-results
	if !errors.Is(worker.err, nodes.ErrInvalidResult) {
		t.Fatalf("error = %v, want ErrInvalidResult", worker.err)
	}
}
