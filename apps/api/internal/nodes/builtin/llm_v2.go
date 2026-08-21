package builtin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	maxLLMV2Fields           = 32
	maxLLMV2FieldLabelRunes  = 80
	maxLLMV2DescriptionRunes = 500
	maxLLMV2StructuredBytes  = 1 << 20
	llmV2RequestTimeout      = 60 * time.Second
)

var llmV2FieldKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

var ErrLLMV2OutputInvalid = errors.New("LLM v2 structured output is invalid")

var llmV2ConfigSchema = agentnode.MustSchema(`{
  "type":"object",
  "properties":{
    "model":{"type":"string","title":"模型"},
    "systemPrompt":{"type":"string","title":"系统提示词","x-ui-widget":"textarea"},
    "temperature":{"type":"number","minimum":0,"maximum":2,"default":0.7,"title":"温度"},
    "maxTokens":{"type":"integer","minimum":1,"maximum":32768,"default":1024,"title":"最大 Token"},
    "outputMode":{"type":"string","enum":["text","structured"],"default":"text","title":"输出模式"},
    "fields":{
      "type":"array",
      "title":"输出字段",
      "default":[],
      "maxItems":32,
      "items":{
        "type":"object",
        "properties":{
          "key":{"type":"string","title":"字段 Key","minLength":1,"maxLength":64,"pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},
          "label":{"type":"string","title":"字段名称","minLength":1,"maxLength":80},
          "description":{"type":"string","title":"字段说明","maxLength":500,"x-ui-widget":"textarea"},
          "type":{"type":"string","title":"字段类型","enum":["string","number","integer","boolean","string_array"]},
          "required":{"type":"boolean","title":"必填","default":true}
        },
        "required":["key","label","type"],
        "additionalProperties":false
      }
    }
  },
  "additionalProperties":false
}`)

type llmV2Node struct {
	provider     modelprovider.Provider
	defaultModel string
}

type llmV2FieldType string

const (
	llmV2FieldString      llmV2FieldType = "string"
	llmV2FieldNumber      llmV2FieldType = "number"
	llmV2FieldInteger     llmV2FieldType = "integer"
	llmV2FieldBoolean     llmV2FieldType = "boolean"
	llmV2FieldStringArray llmV2FieldType = "string_array"
)

type llmV2Field struct {
	Key         string         `json:"key"`
	Label       string         `json:"label"`
	Description string         `json:"description,omitempty"`
	Type        llmV2FieldType `json:"type"`
	Required    *bool          `json:"required,omitempty"`
}

type llmV2Config struct {
	Model        string       `json:"model,omitempty"`
	SystemPrompt string       `json:"systemPrompt,omitempty"`
	Temperature  *float64     `json:"temperature,omitempty"`
	MaxTokens    *int         `json:"maxTokens,omitempty"`
	OutputMode   string       `json:"outputMode"`
	Fields       []llmV2Field `json:"fields,omitempty"`
}

type llmV2Schema struct {
	Type                 string                `json:"type"`
	Properties           llmV2SchemaProperties `json:"properties"`
	Required             []string              `json:"required"`
	AdditionalProperties bool                  `json:"additionalProperties"`
}

type llmV2SchemaProperties []llmV2SchemaProperty

type llmV2SchemaProperty struct {
	Key    string
	Schema llmV2PropertySchema
}

type llmV2PropertySchema struct {
	Type        any                      `json:"type"`
	Description string                   `json:"description,omitempty"`
	Items       *llmV2PropertyItemSchema `json:"items,omitempty"`
}

type llmV2PropertyItemSchema struct {
	Type string `json:"type"`
}

func (properties llmV2SchemaProperties) MarshalJSON() ([]byte, error) {
	var encoded bytes.Buffer
	encoded.WriteByte('{')
	for index, property := range properties {
		if index > 0 {
			encoded.WriteByte(',')
		}
		keyJSON, err := json.Marshal(property.Key)
		if err != nil {
			return nil, err
		}
		valueJSON, err := json.Marshal(property.Schema)
		if err != nil {
			return nil, err
		}
		encoded.Write(keyJSON)
		encoded.WriteByte(':')
		encoded.Write(valueJSON)
	}
	encoded.WriteByte('}')
	return encoded.Bytes(), nil
}

func buildLLMV2Schema(fields []llmV2Field) (json.RawMessage, string, error) {
	if len(fields) == 0 || len(fields) > maxLLMV2Fields {
		return nil, "", invalidLLMV2Fields()
	}
	properties := make(llmV2SchemaProperties, 0, len(fields))
	required := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if !validLLMV2Field(field) {
			return nil, "", invalidLLMV2Fields()
		}
		if _, duplicate := seen[field.Key]; duplicate {
			return nil, "", invalidLLMV2Fields()
		}
		seen[field.Key] = struct{}{}
		propertyType := any(string(field.Type))
		if !llmV2FieldRequired(field) {
			propertyType = []string{string(field.Type), "null"}
		}
		property := llmV2PropertySchema{Type: propertyType, Description: field.Description}
		if field.Type == llmV2FieldStringArray {
			baseType := "array"
			if llmV2FieldRequired(field) {
				property.Type = baseType
			} else {
				property.Type = []string{baseType, "null"}
			}
			property.Items = &llmV2PropertyItemSchema{Type: "string"}
		}
		properties = append(properties, llmV2SchemaProperty{Key: field.Key, Schema: property})
		required = append(required, field.Key)
	}
	encoded, err := json.Marshal(llmV2Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	})
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode output schema", ErrInvalidConfig)
	}
	digest := sha256.Sum256(encoded)
	return json.RawMessage(encoded), fmt.Sprintf("agent_studio_%x", digest[:8]), nil
}

func validLLMV2Field(field llmV2Field) bool {
	if !llmV2FieldKeyPattern.MatchString(field.Key) || field.Key == "text" || field.Key == "json" || field.Key == "usage" {
		return false
	}
	labelRunes := utf8.RuneCountInString(field.Label)
	if labelRunes == 0 || labelRunes > maxLLMV2FieldLabelRunes || utf8.RuneCountInString(field.Description) > maxLLMV2DescriptionRunes {
		return false
	}
	switch field.Type {
	case llmV2FieldString, llmV2FieldNumber, llmV2FieldInteger, llmV2FieldBoolean, llmV2FieldStringArray:
		return true
	default:
		return false
	}
}

func llmV2OutputPorts(config llmV2Config) []agentnode.Port {
	ports := make([]agentnode.Port, 0, len(config.Fields)+2)
	ports = append(ports, agentnode.Port{Key: "json", Title: "结构化结果", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne})
	for _, field := range config.Fields {
		dataType := agentnode.DataTypeAny
		if llmV2FieldRequired(field) {
			switch field.Type {
			case llmV2FieldString:
				dataType = agentnode.DataTypeString
			case llmV2FieldNumber, llmV2FieldInteger:
				dataType = agentnode.DataTypeNumber
			case llmV2FieldBoolean:
				dataType = agentnode.DataTypeBoolean
			case llmV2FieldStringArray:
				dataType = agentnode.DataTypeJSON
			}
		}
		ports = append(ports, agentnode.Port{Key: field.Key, Title: field.Label, Type: dataType, Cardinality: agentnode.CardinalityOne})
	}
	return append(ports, agentnode.Port{Key: "usage", Title: "用量", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne})
}

func llmV2FieldRequired(field llmV2Field) bool {
	return field.Required == nil || *field.Required
}

func invalidLLMV2Fields() error {
	return fmt.Errorf("%w: invalid llm v2 fields", ErrInvalidConfig)
}

func NewLLMV2(provider modelprovider.Provider, defaultModel string) agentnode.Node {
	return &llmV2Node{provider: provider, defaultModel: defaultModel}
}

func (*llmV2Node) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:         "llm",
		Version:      "2",
		Title:        "LLM · 结构化输出",
		Description:  "调用已配置的模型服务生成文本或严格结构化结果",
		Category:     "AI",
		ConfigSchema: append(json.RawMessage(nil), llmV2ConfigSchema...),
		Inputs: []agentnode.Port{{
			Key:         "prompt",
			Title:       "提示词",
			Type:        agentnode.DataTypeString,
			Required:    true,
			Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: textModeOutputs(),
		Capabilities: []agentnode.Capability{
			agentnode.CapabilityNetwork,
			agentnode.CapabilitySecrets,
		},
	}
}

func (node *llmV2Node) Resolve(raw json.RawMessage) (agentnode.ResolvedPorts, error) {
	config, err := node.parseConfig(raw)
	if err != nil {
		return agentnode.ResolvedPorts{}, nodeConfigError(err)
	}
	definition := node.Definition()
	outputs := definition.Outputs
	if config.OutputMode == "structured" {
		outputs = llmV2OutputPorts(config)
	}
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: outputs}, nil
}

func (node *llmV2Node) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if err := checkContext(ctx); err != nil {
		return agentnode.Result{}, err
	}
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
	providerRequest := modelprovider.Request{
		Model:        config.Model,
		SystemPrompt: config.SystemPrompt,
		Prompt:       prompt,
		Temperature:  temperature,
		MaxTokens:    maxTokens,
	}
	var schemaJSON json.RawMessage
	if config.OutputMode == "structured" {
		var schemaName string
		schemaJSON, schemaName, err = buildLLMV2Schema(config.Fields)
		if err != nil {
			return agentnode.Result{}, nodeConfigError(err)
		}
		providerRequest.ResponseFormat = &modelprovider.JSONSchemaFormat{
			Name:        schemaName,
			Description: "Agent Studio structured output",
			Schema:      append(json.RawMessage(nil), schemaJSON...),
			Strict:      true,
		}
	}
	requestContext, cancel := context.WithTimeout(ctx, llmV2RequestTimeout)
	defer cancel()
	response, err := node.provider.Complete(requestContext, providerRequest)
	if err != nil {
		if contextErr := requestContext.Err(); contextErr != nil {
			return agentnode.Result{}, classifyLLMV2ProviderError(contextErr, config.OutputMode == "structured")
		}
		return agentnode.Result{}, classifyLLMV2ProviderError(err, config.OutputMode == "structured")
	}
	if contextErr := requestContext.Err(); contextErr != nil {
		return agentnode.Result{}, classifyLLMV2ProviderError(contextErr, config.OutputMode == "structured")
	}
	if config.OutputMode == "text" {
		return agentnode.Result{Outputs: map[string]any{"text": response.Text, "usage": response.Usage}}, nil
	}
	object, err := decodeLLMV2StructuredOutput(response.Text, schemaJSON)
	if err != nil {
		return agentnode.Result{}, llmV2InternalError("model_output_invalid", ErrLLMV2OutputInvalid)
	}
	outputs := make(map[string]any, len(config.Fields)+2)
	outputs["json"] = object
	for _, field := range config.Fields {
		outputs[field.Key] = object[field.Key]
	}
	outputs["usage"] = response.Usage
	return agentnode.Result{Outputs: outputs}, nil
}

func (node *llmV2Node) parseConfig(raw json.RawMessage) (llmV2Config, error) {
	var config llmV2Config
	if err := decodeConfig(raw, &config); err != nil {
		return llmV2Config{}, err
	}
	if config.Model == "" {
		config.Model = node.defaultModel
	}
	if config.Model == "" {
		return llmV2Config{}, ErrLLMModelMissing
	}
	if config.Temperature != nil && (*config.Temperature < 0 || *config.Temperature > 2) {
		return llmV2Config{}, fmt.Errorf("%w: temperature must be between 0 and 2", ErrInvalidConfig)
	}
	if config.MaxTokens != nil && (*config.MaxTokens < 1 || *config.MaxTokens > 32768) {
		return llmV2Config{}, fmt.Errorf("%w: maxTokens must be between 1 and 32768", ErrInvalidConfig)
	}
	if config.OutputMode == "" {
		config.OutputMode = "text"
	}
	if config.OutputMode != "text" && config.OutputMode != "structured" {
		return llmV2Config{}, fmt.Errorf("%w: outputMode must be text or structured", ErrInvalidConfig)
	}
	if config.OutputMode == "structured" || len(config.Fields) > 0 {
		if _, _, err := buildLLMV2Schema(config.Fields); err != nil {
			return llmV2Config{}, err
		}
	}
	return config, nil
}

func textModeOutputs() []agentnode.Port {
	return []agentnode.Port{
		{Key: "text", Title: "文本", Type: agentnode.DataTypeString, Cardinality: agentnode.CardinalityOne},
		{Key: "usage", Title: "用量", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
	}
}

func decodeLLMV2StructuredOutput(raw string, schemaJSON json.RawMessage) (map[string]any, error) {
	if len(raw) > maxLLMV2StructuredBytes {
		return nil, ErrLLMV2OutputInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrLLMV2OutputInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrLLMV2OutputInvalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrLLMV2OutputInvalid
	}
	const resource = "urn:agent-studio:llm-v2-output"
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, ErrLLMV2OutputInvalid
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, ErrLLMV2OutputInvalid
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, ErrLLMV2OutputInvalid
	}
	if err := schema.Validate(object); err != nil {
		return nil, ErrLLMV2OutputInvalid
	}
	return object, nil
}

func classifyLLMV2ProviderError(err error, structured bool) error {
	if errors.Is(err, context.Canceled) {
		return nodeCanceledError(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nodeTemporaryError(err)
	}
	var providerError *modelprovider.ProviderError
	if errors.As(err, &providerError) {
		switch {
		case providerError.StatusCode == http.StatusTooManyRequests || providerError.StatusCode >= 500 && providerError.StatusCode <= 599:
			return agentnode.NewError(agentnode.ErrorKindTemporary, "upstream_unavailable", err, nil)
		case providerError.StatusCode == http.StatusUnauthorized || providerError.StatusCode == http.StatusForbidden:
			return llmV2InternalError("model_provider_auth_failed", err)
		case structured && (providerError.StatusCode == http.StatusBadRequest || providerError.StatusCode == http.StatusNotFound || providerError.StatusCode == http.StatusUnprocessableEntity):
			return llmV2InternalError("model_structured_output_rejected", err)
		}
	}
	if errors.Is(err, modelprovider.ErrModelRefused) {
		return llmV2InternalError("model_refused", err)
	}
	if structured && (errors.Is(err, modelprovider.ErrInvalidResponse) || errors.Is(err, modelprovider.ErrResponseTooLarge)) {
		return llmV2InternalError("model_output_invalid", err)
	}
	return nodeExecutionError(err)
}

func llmV2InternalError(code string, err error) error {
	return agentnode.NewError(agentnode.ErrorKindInternal, code, err, nil)
}
