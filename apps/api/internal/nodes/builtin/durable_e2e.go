//go:build durablee2e

package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/yyl1212/agent-studio/internal/nodepackage"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const durableE2ENodeVersion = "1"

type durableE2ESlowNode struct {
	nodeType string
	safety   agentnode.ExecutionSafety
}

type durableE2ESlowConfig struct {
	DelayMS int `json:"delayMs"`
}

func durableE2ENodeRefs() []nodepackage.NodeRef {
	return []nodepackage.NodeRef{
		{Type: "e2e.slow-pure", Version: durableE2ENodeVersion},
		{Type: "e2e.slow-side-effect", Version: durableE2ENodeVersion},
	}
}

func durableE2ENodes() []agentnode.Node {
	return []agentnode.Node{
		&durableE2ESlowNode{nodeType: "e2e.slow-pure", safety: agentnode.ExecutionSafetyPure},
		&durableE2ESlowNode{nodeType: "e2e.slow-side-effect", safety: agentnode.ExecutionSafetySideEffect},
	}
}

func (node *durableE2ESlowNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type: node.nodeType, Version: durableE2ENodeVersion, Title: "Durable E2E Slow Node",
		Description: "仅在 durablee2e 构建标签下注册的故障注入节点", Category: "E2E",
		ExecutionSafety: node.safety,
		ConfigSchema:    agentnode.MustSchema(`{"type":"object","properties":{"delayMs":{"type":"integer","minimum":1,"maximum":30000}},"required":["delayMs"],"additionalProperties":false}`),
		Inputs:          []agentnode.Port{{Key: "value", Title: "Value", Type: agentnode.DataTypeAny, Required: true, Cardinality: agentnode.CardinalityOne}},
		Outputs:         []agentnode.Port{{Key: "result", Title: "Result", Type: agentnode.DataTypeAny, Cardinality: agentnode.CardinalityOne}},
	}
}

func (node *durableE2ESlowNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if _, err := parseDurableE2ESlowConfig(config); err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	definition := node.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *durableE2ESlowNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	config, err := parseDurableE2ESlowConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, err
	}
	values := request.Inputs["value"]
	if len(values) != 1 {
		return agentnode.Result{}, agentnode.NewError(agentnode.ErrorKindInput, "invalid_e2e_value", errors.New("e2e value must contain exactly one item"), nil)
	}
	timer := time.NewTimer(time.Duration(config.DelayMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return agentnode.Result{}, agentnode.NewError(agentnode.ErrorKindCanceled, "run_canceled", ctx.Err(), nil)
	case <-timer.C:
		return agentnode.Result{Outputs: map[string]any{"result": values[0]}}, nil
	}
}

func parseDurableE2ESlowConfig(raw json.RawMessage) (durableE2ESlowConfig, error) {
	var config durableE2ESlowConfig
	if err := agentnode.DecodeConfig(raw, &config); err != nil {
		return durableE2ESlowConfig{}, err
	}
	if config.DelayMS < 1 || config.DelayMS > 30000 {
		return durableE2ESlowConfig{}, agentnode.NewError(agentnode.ErrorKindConfig, "invalid_e2e_delay", errors.New("e2e delay is out of range"), nil)
	}
	return config, nil
}
