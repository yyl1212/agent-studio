package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentstudio.local/api/internal/domain"
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

func (*codeNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:        "code",
		Version:     "1",
		Title:       "代码",
		Description: "执行受限 Starlark main(input)",
		Category:    "数据",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{"source":{"type":"string","maxLength":65536,"title":"Starlark 源码","x-ui-widget":"code"}},
          "required":["source"],
          "additionalProperties":false
        }`),
		Inputs:  []domain.PortDefinition{{Key: "input", Title: "输入", Type: domain.TypeAny, Cardinality: domain.CardinalityOne}},
		Outputs: []domain.PortDefinition{{Key: "result", Title: "结果", Type: domain.TypeAny, Cardinality: domain.CardinalityOne}},
	}
}

func (node *codeNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
	if _, err := parseCodeConfig(config); err != nil {
		return domain.ResolvedPorts{}, err
	}
	definition := node.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *codeNode) Execute(ctx context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	config, err := parseCodeConfig(request.Config)
	if err != nil {
		return domain.NodeResult{}, err
	}
	input := any(nil)
	if values := request.Inputs["input"]; len(values) > 0 {
		if len(values) != 1 {
			return domain.NodeResult{}, fmt.Errorf("%w: input", ErrInputCardinality)
		}
		input = values[0]
	}
	starlarkInput, err := toStarlark(input, 0)
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("convert code input: %w", err)
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
		return domain.NodeResult{}, mapCodeError(runContext, err)
	}
	mainValue, exists := globals["main"]
	if !exists {
		return domain.NodeResult{}, ErrCodeMainMissing
	}
	callable, ok := mainValue.(starlark.Callable)
	if !ok {
		return domain.NodeResult{}, ErrCodeMainMissing
	}
	result, err := starlark.Call(thread, callable, starlark.Tuple{starlarkInput}, nil)
	if err != nil {
		return domain.NodeResult{}, mapCodeError(runContext, err)
	}
	converted, err := fromStarlark(result, 0)
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("%w: %v", ErrCodeExecution, err)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("%w: output is not JSON-compatible", ErrCodeExecution)
	}
	if len(encoded) > node.maxOutputBytes {
		return domain.NodeResult{}, ErrCodeOutputTooLarge
	}
	return domain.NodeResult{Outputs: map[string]any{"result": converted}}, nil
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
		return fmt.Errorf("%w: %v", ErrCodeTimeout, err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %v", ctx.Err(), err)
	}
	if strings.Contains(err.Error(), "too many steps") {
		return fmt.Errorf("%w: %v", ErrCodeStepLimit, err)
	}
	return fmt.Errorf("%w: %v", ErrCodeExecution, err)
}
