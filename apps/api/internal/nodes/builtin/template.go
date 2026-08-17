package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

var templateVariablePattern = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}`)

type templateNode struct{}

type templateConfig struct {
	Template string `json:"template"`
}

func NewTemplate() *templateNode {
	return &templateNode{}
}

func (*templateNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:        "template",
		Version:     "1",
		Title:       "提示词模板",
		Description: "使用严格占位符组合提示词",
		Category:    "文本",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "template":{"type":"string","title":"模板","x-ui-widget":"textarea"}
          },
          "required":["template"],
          "additionalProperties":false
        }`),
		Outputs: []domain.PortDefinition{{
			Key:         "text",
			Title:       "文本",
			Type:        domain.TypeString,
			Cardinality: domain.CardinalityOne,
		}},
	}
}

func (*templateNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
	_, variables, err := parseTemplateConfig(config)
	if err != nil {
		return domain.ResolvedPorts{}, err
	}
	inputs := make([]domain.PortDefinition, 0, len(variables))
	for _, variable := range variables {
		inputs = append(inputs, domain.PortDefinition{
			Key:         variable,
			Title:       variable,
			Type:        domain.TypeAny,
			Required:    true,
			Cardinality: domain.CardinalityOne,
		})
	}
	return domain.ResolvedPorts{Inputs: inputs, Outputs: NewTemplate().Definition().Outputs}, nil
}

func (*templateNode) Execute(_ context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	config, variables, err := parseTemplateConfig(request.Config)
	if err != nil {
		return domain.NodeResult{}, err
	}
	rendered := config.Template
	for _, variable := range variables {
		value, err := exactlyOneInput(request.Inputs, variable)
		if err != nil {
			return domain.NodeResult{}, err
		}
		text, err := templateValue(value)
		if err != nil {
			return domain.NodeResult{}, fmt.Errorf("render template variable %s: %w", variable, err)
		}
		rendered = strings.ReplaceAll(rendered, "{{"+variable+"}}", text)
	}
	return domain.NodeResult{Outputs: map[string]any{"text": rendered}}, nil
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
