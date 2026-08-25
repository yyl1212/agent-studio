package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

var templateVariablePattern = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}`)

type templateNode struct{}

type templateConfig struct {
	Template string `json:"template"`
}

func NewTemplate() *templateNode {
	return &templateNode{}
}

func (*templateNode) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:            "template",
		Version:         "1",
		Title:           "提示词模板",
		Description:     "使用严格占位符组合提示词",
		Category:        "文本",
		ExecutionSafety: agentnode.ExecutionSafetyPure,
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "template":{"type":"string","title":"模板","x-ui-widget":"textarea"}
          },
          "required":["template"],
          "additionalProperties":false
        }`),
		Outputs: []agentnode.Port{{
			Key:         "text",
			Title:       "文本",
			Type:        agentnode.DataTypeString,
			Cardinality: agentnode.CardinalityOne,
		}},
	}
}

func (*templateNode) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	_, variables, err := parseTemplateConfig(config)
	if err != nil {
		return agentnode.ResolvedPorts{}, nodeConfigError(err)
	}
	inputs := make([]agentnode.Port, 0, len(variables))
	for _, variable := range variables {
		inputs = append(inputs, agentnode.Port{
			Key:         variable,
			Title:       variable,
			Type:        agentnode.DataTypeAny,
			Required:    true,
			Cardinality: agentnode.CardinalityOne,
		})
	}
	return agentnode.ResolvedPorts{Inputs: inputs, Outputs: NewTemplate().Definition().Outputs}, nil
}

func (*templateNode) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if err := checkContext(ctx); err != nil {
		return agentnode.Result{}, err
	}
	config, variables, err := parseTemplateConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, nodeConfigError(err)
	}
	rendered := config.Template
	for _, variable := range variables {
		value, err := exactlyOneInput(request.Inputs, variable)
		if err != nil {
			return agentnode.Result{}, nodeInputError(err)
		}
		text, err := templateValue(value)
		if err != nil {
			return agentnode.Result{}, nodeExecutionError(fmt.Errorf("render template variable %s: %w", variable, err))
		}
		rendered = strings.ReplaceAll(rendered, "{{"+variable+"}}", text)
	}
	return agentnode.Result{Outputs: map[string]any{"text": rendered}}, nil
}

func parseTemplateConfig(raw json.RawMessage) (templateConfig, []string, error) {
	var config templateConfig
	if err := decodeConfig(raw, &config); err != nil {
		return templateConfig{}, nil, err
	}
	matches := templateVariablePattern.FindAllStringSubmatch(config.Template, -1)
	remainder := templateVariablePattern.ReplaceAllString(config.Template, "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		return templateConfig{}, nil, fmt.Errorf("%w: placeholders must use {{identifier}}", ErrInvalidTemplate)
	}
	seen := make(map[string]struct{}, len(matches))
	variables := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		variables = append(variables, match[1])
	}
	return config, variables, nil
}

func templateValue(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("value is not JSON-compatible: %w", err)
	}
	return string(encoded), nil
}
