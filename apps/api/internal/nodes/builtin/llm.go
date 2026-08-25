package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var (
	ErrLLMModelMissing      = errors.New("LLM model is required")
	ErrModelProviderMissing = errors.New("model provider is required")
)

type llmNode struct {
	provider     modelprovider.Provider
	defaultModel string
}

type llmConfig struct {
	Model        string   `json:"model,omitempty"`
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Temperature  *float64 `json:"temperature,omitempty"`
	MaxTokens    *int     `json:"maxTokens,omitempty"`
}

func NewLLM(provider modelprovider.Provider, defaultModel string) *llmNode {
	return &llmNode{provider: provider, defaultModel: defaultModel}
}

func (*llmNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:            "llm",
		Version:         "1",
		Title:           "LLM",
		Description:     "调用已配置的模型服务生成文本",
		Category:        "AI",
		ExecutionSafety: agentnode.ExecutionSafetyReadOnly,
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "model":{"type":"string","title":"模型"},
            "systemPrompt":{"type":"string","title":"系统提示词","x-ui-widget":"textarea"},
            "temperature":{"type":"number","minimum":0,"maximum":2,"default":0.7,"title":"温度"},
            "maxTokens":{"type":"integer","minimum":1,"maximum":32768,"default":1024,"title":"最大 Token"}
          },
          "additionalProperties":false
        }`),
		Inputs: []agentnode.Port{{
			Key:         "prompt",
			Title:       "提示词",
			Type:        agentnode.DataTypeString,
			Required:    true,
			Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: []agentnode.Port{
			{Key: "text", Title: "文本", Type: agentnode.DataTypeString, Cardinality: agentnode.CardinalityOne},
			{Key: "usage", Title: "用量", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
		},
		Capabilities: []agentnode.Capability{
			agentnode.CapabilityNetwork,
			agentnode.CapabilitySecrets,
		},
	}
}

func (node *llmNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if _, err := node.parseConfig(config); err != nil {
		return agentnode.ResolvedPorts{}, nodeConfigError(err)
	}
	definition := node.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *llmNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if node.provider == nil {
		return agentnode.Result{}, nodeExecutionError(ErrModelProviderMissing)
	}
	config, err := node.parseConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, nodeConfigError(err)
	}
	promptValue, err := exactlyOneInput(request.Inputs, "prompt")
	if err != nil {
		if errors.Is(err, ErrRequiredInputMissing) {
			return agentnode.Result{}, nodeMissingInputError(err)
		}
		return agentnode.Result{}, nodeInputError(err)
	}
	prompt, ok := promptValue.(string)
	if !ok {
		return agentnode.Result{}, nodeInputError(fmt.Errorf("%w: prompt", ErrInputTypeMismatch))
	}

	temperature := 0.7
	if config.Temperature != nil {
		temperature = *config.Temperature
	}
	maxTokens := 1024
	if config.MaxTokens != nil {
		maxTokens = *config.MaxTokens
	}
	requestContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	response, err := node.provider.Complete(requestContext, modelprovider.Request{
		Model:        config.Model,
		SystemPrompt: config.SystemPrompt,
		Prompt:       prompt,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
	})
	if err != nil {
		return agentnode.Result{}, classifyExternalError(err)
	}
	return agentnode.Result{Outputs: map[string]any{
		"text":  response.Text,
		"usage": response.Usage,
	}}, nil
}

func (node *llmNode) parseConfig(raw json.RawMessage) (llmConfig, error) {
	var config llmConfig
	if err := decodeConfig(raw, &config); err != nil {
		return llmConfig{}, err
	}
	if config.Model == "" {
		config.Model = node.defaultModel
	}
	if config.Model == "" {
		return llmConfig{}, ErrLLMModelMissing
	}
	if config.Temperature != nil && (*config.Temperature < 0 || *config.Temperature > 2) {
		return llmConfig{}, fmt.Errorf("%w: temperature must be between 0 and 2", ErrInvalidConfig)
	}
	if config.MaxTokens != nil && (*config.MaxTokens < 1 || *config.MaxTokens > 32768) {
		return llmConfig{}, fmt.Errorf("%w: maxTokens must be between 1 and 32768", ErrInvalidConfig)
	}
	return config, nil
}
