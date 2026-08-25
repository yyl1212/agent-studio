package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"go.starlark.net/starlark"
)

const maxCodeSourceBytes = 64 << 10

var (
	ErrCodeSourceTooLarge = errors.New("code source too large")
	ErrCodeMainMissing    = errors.New("code main function missing")
	ErrCodeStepLimit      = errors.New("code step limit exceeded")
	ErrCodeTimeout        = errors.New("code execution timed out")
	ErrCodeOutputTooLarge = errors.New("code output too large")
	ErrCodeExecution      = errors.New("code execution failed")
)

type CodeOptions struct {
	MaxSteps       uint64
	Timeout        time.Duration
	MaxOutputBytes int
}

type codeNode struct {
	maxSteps       uint64
	timeout        time.Duration
	maxOutputBytes int
}

type codeConfig struct {
	Source string `json:"source"`
}

func NewCode(options CodeOptions) *codeNode {
	if options.MaxSteps == 0 {
		options.MaxSteps = 100000
	}
	if options.Timeout <= 0 {
		options.Timeout = 2 * time.Second
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = 1 << 20
	}
	return &codeNode{
		maxSteps:       options.MaxSteps,
		timeout:        options.Timeout,
		maxOutputBytes: options.MaxOutputBytes,
	}
}

func (*codeNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:            "code",
		Version:         "1",
		Title:           "代码",
		Description:     "执行受限 Starlark main(input)",
		Category:        "数据",
		ExecutionSafety: agentnode.ExecutionSafetyPure,
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{"source":{"type":"string","maxLength":65536,"title":"Starlark 源码","x-ui-widget":"code"}},
          "required":["source"],
          "additionalProperties":false
        }`),
		Inputs:       []agentnode.Port{{Key: "input", Title: "输入", Type: agentnode.DataTypeAny, Cardinality: agentnode.CardinalityOne}},
		Outputs:      []agentnode.Port{{Key: "result", Title: "结果", Type: agentnode.DataTypeAny, Cardinality: agentnode.CardinalityOne}},
		Capabilities: []agentnode.Capability{},
	}
}

func (node *codeNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if _, err := parseCodeConfig(config); err != nil {
		return agentnode.ResolvedPorts{}, nodeConfigError(err)
	}
	definition := node.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *codeNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	config, err := parseCodeConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, nodeConfigError(err)
	}
	input := any(nil)
	if values := request.Inputs["input"]; len(values) > 0 {
		if len(values) != 1 {
			return agentnode.Result{}, nodeInputError(fmt.Errorf("%w: input", ErrInputCardinality))
		}
		input = values[0]
	}
	starlarkInput, err := toStarlark(input, 0)
	if err != nil {
		return agentnode.Result{}, nodeInputError(fmt.Errorf("convert code input: %w", err))
	}

	thread := &starlark.Thread{Name: "agent-studio-code"}
	thread.SetMaxExecutionSteps(node.maxSteps)
	runContext, cancel := context.WithTimeout(ctx, node.timeout)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-runContext.Done():
			thread.Cancel("agent-studio context cancelled")
		case <-done:
		}
	}()

	globals, err := starlark.ExecFile(thread, "node.star", config.Source, nil)
	if err != nil {
		return agentnode.Result{}, mapCodeError(runContext, err)
	}
	mainValue, exists := globals["main"]
	if !exists {
		return agentnode.Result{}, nodeExecutionError(ErrCodeMainMissing)
	}
	callable, ok := mainValue.(starlark.Callable)
	if !ok {
		return agentnode.Result{}, nodeExecutionError(ErrCodeMainMissing)
	}
	result, err := starlark.Call(thread, callable, starlark.Tuple{starlarkInput}, nil)
	if err != nil {
		return agentnode.Result{}, mapCodeError(runContext, err)
	}
	converted, err := fromStarlark(result, 0)
	if err != nil {
		return agentnode.Result{}, nodeExecutionError(fmt.Errorf("%w: %v", ErrCodeExecution, err))
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return agentnode.Result{}, nodeExecutionError(fmt.Errorf("%w: output is not JSON-compatible", ErrCodeExecution))
	}
	if len(encoded) > node.maxOutputBytes {
		return agentnode.Result{}, nodeExecutionError(ErrCodeOutputTooLarge)
	}
	return agentnode.Result{Outputs: map[string]any{"result": converted}}, nil
}

func parseCodeConfig(raw json.RawMessage) (codeConfig, error) {
	var config codeConfig
	if err := decodeConfig(raw, &config); err != nil {
		return codeConfig{}, err
	}
	if len(config.Source) > maxCodeSourceBytes {
		return codeConfig{}, ErrCodeSourceTooLarge
	}
	return config, nil
}

func mapCodeError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nodeTemporaryError(fmt.Errorf("%w: %v", ErrCodeTimeout, err))
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nodeCanceledError(fmt.Errorf("%w: %v", context.Canceled, err))
	}
	if strings.Contains(err.Error(), "too many steps") {
		return nodeExecutionError(fmt.Errorf("%w: %v", ErrCodeStepLimit, err))
	}
	return nodeExecutionError(fmt.Errorf("%w: %v", ErrCodeExecution, err))
}
