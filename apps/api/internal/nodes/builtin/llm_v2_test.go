package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const llmV2AnswerConfig = `{"outputMode":"structured","fields":[{"key":"answer","label":"回答","type":"string","required":true}]}`

type llmV2RecordingProvider struct {
	request  modelprovider.Request
	response modelprovider.Response
	err      error
	deadline time.Time
}

type llmV2CancelingProvider struct {
	cancel context.CancelFunc
}

func (provider llmV2CancelingProvider) Complete(context.Context, modelprovider.Request) (modelprovider.Response, error) {
	provider.cancel()
	return modelprovider.Response{Text: "ignored"}, nil
}

func (provider *llmV2RecordingProvider) Complete(ctx context.Context, request modelprovider.Request) (modelprovider.Response, error) {
	provider.request = request
	provider.deadline, _ = ctx.Deadline()
	return provider.response, provider.err
}

func TestBuildLLMV2SchemaIsStableOrderedAndNullable(t *testing.T) {
	fields := []llmV2Field{
		{Key: "name", Label: "姓名", Type: llmV2FieldString, Required: boolPointer(true)},
		{Key: "age", Label: "年龄", Type: llmV2FieldInteger, Required: boolPointer(false)},
		{Key: "tags", Label: "标签", Type: llmV2FieldStringArray, Required: boolPointer(true)},
	}
	first, firstName, err := buildLLMV2Schema(fields)
	if err != nil {
		t.Fatal(err)
	}
	second, secondName, err := buildLLMV2Schema(fields)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstName != secondName {
		t.Fatal("schema drifted")
	}
	const wantSchema = `{"type":"object","properties":{"name":{"type":"string"},"age":{"type":["integer","null"]},"tags":{"type":"array","items":{"type":"string"}}},"required":["name","age","tags"],"additionalProperties":false}`
	if string(first) != wantSchema {
		t.Fatalf("schema=%s", first)
	}
	if firstName != "agent_studio_e57404d9d60b63dd" {
		t.Fatalf("name=%q", firstName)
	}
	var schema map[string]any
	if err := json.Unmarshal(first, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	if !reflect.DeepEqual(properties["age"].(map[string]any)["type"], []any{"integer", "null"}) {
		t.Fatalf("age=%v", properties["age"])
	}
}

func TestBuildLLMV2SchemaIncludesDescriptions(t *testing.T) {
	schema, _, err := buildLLMV2Schema([]llmV2Field{{
		Key: "answer", Label: "回答", Description: "最终回答", Type: llmV2FieldString,
	}})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"object","properties":{"answer":{"type":"string","description":"最终回答"}},"required":["answer"],"additionalProperties":false}`
	if string(schema) != want {
		t.Fatalf("schema=%s", schema)
	}
}

func TestBuildLLMV2SchemaRejectsInvalidFields(t *testing.T) {
	valid := llmV2Field{Key: "answer", Label: "回答", Type: llmV2FieldString}
	thirtyThree := make([]llmV2Field, 33)
	for index := range thirtyThree {
		thirtyThree[index] = llmV2Field{Key: "field" + strconv.Itoa(index), Label: "字段", Type: llmV2FieldString}
	}
	tests := []struct {
		name   string
		fields []llmV2Field
	}{
		{name: "empty", fields: nil},
		{name: "too many", fields: thirtyThree},
		{name: "duplicate", fields: []llmV2Field{valid, valid}},
		{name: "reserved text", fields: []llmV2Field{{Key: "text", Label: "文本", Type: llmV2FieldString}}},
		{name: "reserved json", fields: []llmV2Field{{Key: "json", Label: "JSON", Type: llmV2FieldString}}},
		{name: "reserved usage", fields: []llmV2Field{{Key: "usage", Label: "用量", Type: llmV2FieldString}}},
		{name: "invalid key", fields: []llmV2Field{{Key: "bad-key", Label: "字段", Type: llmV2FieldString}}},
		{name: "key too long", fields: []llmV2Field{{Key: "a" + strings.Repeat("b", 64), Label: "字段", Type: llmV2FieldString}}},
		{name: "empty label", fields: []llmV2Field{{Key: "answer", Label: "", Type: llmV2FieldString}}},
		{name: "label too long", fields: []llmV2Field{{Key: "answer", Label: strings.Repeat("界", 81), Type: llmV2FieldString}}},
		{name: "description too long", fields: []llmV2Field{{Key: "answer", Label: "回答", Description: strings.Repeat("界", 501), Type: llmV2FieldString}}},
		{name: "unknown type", fields: []llmV2Field{{Key: "answer", Label: "回答", Type: llmV2FieldType("object")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := buildLLMV2Schema(test.fields)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestBuildLLMV2SchemaAcceptsExactBudgets(t *testing.T) {
	fields := make([]llmV2Field, 32)
	for index := range fields {
		fields[index] = llmV2Field{Key: "field" + strconv.Itoa(index), Label: "字段", Type: llmV2FieldString}
	}
	fields[0] = llmV2Field{
		Key:         "a" + strings.Repeat("b", 63),
		Label:       strings.Repeat("界", 80),
		Description: strings.Repeat("界", 500),
		Type:        llmV2FieldString,
	}
	if _, _, err := buildLLMV2Schema(fields); err != nil {
		t.Fatalf("exact budgets rejected: %v", err)
	}
}

func TestLLMV2OutputPortsPreserveFieldOrderAndTypes(t *testing.T) {
	ports := llmV2OutputPorts(llmV2Config{Fields: []llmV2Field{
		{Key: "name", Label: "姓名", Type: llmV2FieldString, Required: boolPointer(true)},
		{Key: "score", Label: "分数", Type: llmV2FieldNumber, Required: boolPointer(true)},
		{Key: "count", Label: "数量", Type: llmV2FieldInteger, Required: boolPointer(true)},
		{Key: "enabled", Label: "启用", Type: llmV2FieldBoolean, Required: boolPointer(true)},
		{Key: "tags", Label: "标签", Type: llmV2FieldStringArray, Required: boolPointer(true)},
		{Key: "note", Label: "备注", Type: llmV2FieldString, Required: boolPointer(false)},
	}})
	want := []agentnode.Port{
		{Key: "json", Title: "结构化结果", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
		{Key: "name", Title: "姓名", Type: agentnode.DataTypeString, Cardinality: agentnode.CardinalityOne},
		{Key: "score", Title: "分数", Type: agentnode.DataTypeNumber, Cardinality: agentnode.CardinalityOne},
		{Key: "count", Title: "数量", Type: agentnode.DataTypeNumber, Cardinality: agentnode.CardinalityOne},
		{Key: "enabled", Title: "启用", Type: agentnode.DataTypeBoolean, Cardinality: agentnode.CardinalityOne},
		{Key: "tags", Title: "标签", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
		{Key: "note", Title: "备注", Type: agentnode.DataTypeAny, Cardinality: agentnode.CardinalityOne},
		{Key: "usage", Title: "用量", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports=%+v", ports)
	}
}

func boolPointer(value bool) *bool { return &value }

func TestLLMV2DefinitionAndResolveModes(t *testing.T) {
	node := NewLLMV2(modelprovider.NewMock(), "mock")
	definition := node.Definition()
	if definition.Type != "llm" || definition.Version != "2" || definition.Title != "LLM · 结构化输出" || definition.Category != "AI" {
		t.Fatalf("definition=%+v", definition)
	}
	if !reflect.DeepEqual(definition.Capabilities, []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}) {
		t.Fatalf("capabilities=%v", definition.Capabilities)
	}
	textPorts, err := node.Resolve(json.RawMessage(`{"outputMode":"text"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertPortKeys(t, textPorts.Outputs, "text", "usage")
	structuredPorts, err := node.Resolve(json.RawMessage(llmV2AnswerConfig))
	if err != nil {
		t.Fatal(err)
	}
	assertPortKeys(t, structuredPorts.Outputs, "json", "answer", "usage")
}

func TestLLMV2DefinitionDoesNotExposeMutableSchema(t *testing.T) {
	node := NewLLMV2(modelprovider.NewMock(), "mock")
	definition := node.Definition()
	wantFirstByte := definition.ConfigSchema[0]
	definition.ConfigSchema[0] = 'x'
	gotFirstByte := node.Definition().ConfigSchema[0]
	definition.ConfigSchema[0] = wantFirstByte
	if gotFirstByte != wantFirstByte {
		t.Fatalf("ConfigSchema mutation escaped Definition: got %q want %q", gotFirstByte, wantFirstByte)
	}
}

func TestLLMV2AppliesDefaultsAndValidatesConfig(t *testing.T) {
	node := NewLLMV2(modelprovider.NewMock(), "default-model")
	for _, config := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"temperature":0,"maxTokens":1,"outputMode":"text"}`),
		json.RawMessage(`{"temperature":2,"maxTokens":32768,"outputMode":"text","fields":[{"key":"saved","label":"保留","type":"boolean"}]}`),
	} {
		ports, err := node.Resolve(config)
		if err != nil {
			t.Fatalf("valid config %s: %v", config, err)
		}
		assertPortKeys(t, ports.Outputs, "text", "usage")
	}
	invalid := []json.RawMessage{
		json.RawMessage(`{"temperature":-0.1}`),
		json.RawMessage(`{"temperature":2.1}`),
		json.RawMessage(`{"maxTokens":0}`),
		json.RawMessage(`{"maxTokens":32769}`),
		json.RawMessage(`{"outputMode":"unknown"}`),
		json.RawMessage(`{"outputMode":"structured","fields":[]}`),
		json.RawMessage(`{"outputMode":"text","fields":[{"key":"bad-key","label":"坏字段","type":"string"}]}`),
	}
	for _, config := range invalid {
		if _, err := node.Resolve(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("config %s error=%v", config, err)
		}
	}
	if _, err := NewLLMV2(modelprovider.NewMock(), "").Resolve(json.RawMessage(`{}`)); !errors.Is(err, ErrLLMModelMissing) {
		t.Fatalf("missing model error=%v", err)
	}

	registry := nodes.NewRegistry()
	if err := registry.Register(node); err != nil {
		t.Fatal(err)
	}
	if err := registry.ValidateConfig("llm", "2", json.RawMessage(`{"apiKey":"secret"}`)); err == nil {
		t.Fatal("expected API key config to be rejected")
	}
}

func TestLLMV2ExecutesTextWithoutResponseFormat(t *testing.T) {
	provider := &llmV2RecordingProvider{response: modelprovider.Response{Text: "plain", Usage: map[string]int{"totalTokens": 2}}}
	node := NewLLMV2(provider, "default-model")
	result, err := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.request.ResponseFormat != nil || provider.request.Model != "default-model" || provider.request.Prompt != "hello" {
		t.Fatalf("request=%+v", provider.request)
	}
	if result.Outputs["text"] != "plain" || !reflect.DeepEqual(result.Outputs["usage"], map[string]int{"totalTokens": 2}) {
		t.Fatalf("outputs=%v", result.Outputs)
	}
}

func TestLLMV2ExecutesAndProjectsStructuredOutput(t *testing.T) {
	provider := &llmV2RecordingProvider{response: modelprovider.Response{
		Text:  `{"answer":"ok","score":9007199254740993}`,
		Usage: map[string]int{"totalTokens": 7},
	}}
	node := NewLLMV2(provider, "default-model")
	started := time.Now()
	result, err := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"model":"chosen","systemPrompt":"system","temperature":0.2,"maxTokens":128,"outputMode":"structured","fields":[{"key":"answer","label":"回答","type":"string","required":true},{"key":"score","label":"分数","type":"integer","required":true}]}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.request.ResponseFormat == nil || !provider.request.ResponseFormat.Strict || provider.request.ResponseFormat.Name == "" {
		t.Fatalf("request=%+v", provider.request)
	}
	if provider.request.Model != "chosen" || provider.request.SystemPrompt != "system" || provider.request.Temperature != 0.2 || provider.request.MaxTokens != 128 {
		t.Fatalf("request=%+v", provider.request)
	}
	remaining := time.Until(provider.deadline)
	if provider.deadline.Before(started) || remaining < 55*time.Second || remaining > 60*time.Second {
		t.Fatalf("deadline remaining=%s", remaining)
	}
	if result.Outputs["answer"] != "ok" || result.Outputs["score"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("outputs=%v", result.Outputs)
	}
	object := result.Outputs["json"].(map[string]any)
	if object["score"].(json.Number).String() != "9007199254740993" || !reflect.DeepEqual(result.Outputs["usage"], map[string]int{"totalTokens": 7}) {
		t.Fatalf("outputs=%v", result.Outputs)
	}
}

func TestLLMV2ReturnsNullableFieldsAsPresentNilOutputs(t *testing.T) {
	provider := &llmV2RecordingProvider{response: modelprovider.Response{Text: `{"note":null}`}}
	node := NewLLMV2(provider, "model")
	result, err := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"outputMode":"structured","fields":[{"key":"note","label":"备注","type":"string","required":false}]}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, exists := result.Outputs["note"]
	if !exists || value != nil {
		t.Fatalf("outputs=%v", result.Outputs)
	}
}

func TestLLMV2RejectsInvalidStructuredOutputsWithoutLeakingContent(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		response string
	}{
		{name: "empty", config: llmV2AnswerConfig, response: ""},
		{name: "null root", config: llmV2AnswerConfig, response: `null`},
		{name: "array root", config: llmV2AnswerConfig, response: `[]`},
		{name: "markdown fence", config: llmV2AnswerConfig, response: "```json\n{\"answer\":\"ok\"}\n```"},
		{name: "trailing value", config: llmV2AnswerConfig, response: `{"answer":"ok"}{"answer":"second"}`},
		{name: "extra field", config: llmV2AnswerConfig, response: `{"answer":"ok","extra":"SENSITIVE"}`},
		{name: "missing field", config: llmV2AnswerConfig, response: `{}`},
		{name: "wrong type", config: llmV2AnswerConfig, response: `{"answer":7}`},
		{name: "optional omitted", config: `{"outputMode":"structured","fields":[{"key":"answer","label":"回答","type":"string","required":false}]}`, response: `{}`},
		{name: "over limit", config: llmV2AnswerConfig, response: strings.Repeat("x", 1<<20+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &llmV2RecordingProvider{response: modelprovider.Response{Text: test.response}}
			_, err := NewLLMV2(provider, "model").Execute(context.Background(), agentnode.Request{
				Config: json.RawMessage(test.config),
				Inputs: map[string][]any{"prompt": {"hello"}},
			})
			assertNodeError(t, err, agentnode.ErrorKindInternal, "model_output_invalid")
			if strings.Contains(err.Error(), "SENSITIVE") || strings.Contains(err.Error(), test.response) && test.response != "" {
				t.Fatalf("error leaked model output: %v", err)
			}
		})
	}
}

func TestLLMV2AcceptsStructuredOutputAtExactByteLimit(t *testing.T) {
	const envelopeBytes = len(`{"answer":""}`)
	answer := strings.Repeat("x", 1<<20-envelopeBytes)
	response := fmt.Sprintf(`{"answer":%q}`, answer)
	if len(response) != 1<<20 {
		t.Fatalf("fixture bytes=%d", len(response))
	}
	provider := &llmV2RecordingProvider{response: modelprovider.Response{Text: response}}
	result, err := NewLLMV2(provider, "model").Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(llmV2AnswerConfig),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outputs["answer"].(string)) != len(answer) {
		t.Fatalf("answer length=%d", len(result.Outputs["answer"].(string)))
	}
}

func TestLLMV2ClassifiesProviderErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind agentnode.ErrorKind
		code string
	}{
		{name: "canceled", err: context.Canceled, kind: agentnode.ErrorKindCanceled, code: "run_canceled"},
		{name: "deadline", err: context.DeadlineExceeded, kind: agentnode.ErrorKindTemporary, code: "upstream_timeout"},
		{name: "rate limited", err: &modelprovider.ProviderError{StatusCode: http.StatusTooManyRequests}, kind: agentnode.ErrorKindTemporary, code: "upstream_unavailable"},
		{name: "server error", err: &modelprovider.ProviderError{StatusCode: http.StatusBadGateway}, kind: agentnode.ErrorKindTemporary, code: "upstream_unavailable"},
		{name: "structured unsupported", err: &modelprovider.ProviderError{StatusCode: http.StatusBadRequest}, kind: agentnode.ErrorKindInternal, code: "model_structured_output_rejected"},
		{name: "model missing", err: &modelprovider.ProviderError{StatusCode: http.StatusNotFound}, kind: agentnode.ErrorKindInternal, code: "model_structured_output_rejected"},
		{name: "unprocessable", err: &modelprovider.ProviderError{StatusCode: http.StatusUnprocessableEntity}, kind: agentnode.ErrorKindInternal, code: "model_structured_output_rejected"},
		{name: "unauthorized", err: &modelprovider.ProviderError{StatusCode: http.StatusUnauthorized}, kind: agentnode.ErrorKindInternal, code: "model_provider_auth_failed"},
		{name: "forbidden", err: &modelprovider.ProviderError{StatusCode: http.StatusForbidden}, kind: agentnode.ErrorKindInternal, code: "model_provider_auth_failed"},
		{name: "refused", err: modelprovider.ErrModelRefused, kind: agentnode.ErrorKindInternal, code: "model_refused"},
		{name: "invalid provider response", err: modelprovider.ErrInvalidResponse, kind: agentnode.ErrorKindInternal, code: "model_output_invalid"},
		{name: "provider response too large", err: modelprovider.ErrResponseTooLarge, kind: agentnode.ErrorKindInternal, code: "model_output_invalid"},
		{name: "other", err: errors.New("SENSITIVE provider detail"), kind: agentnode.ErrorKindInternal, code: "execution_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &llmV2RecordingProvider{err: test.err}
			_, err := NewLLMV2(provider, "model").Execute(context.Background(), agentnode.Request{
				Config: json.RawMessage(llmV2AnswerConfig),
				Inputs: map[string][]any{"prompt": {"hello"}},
			})
			assertNodeError(t, err, test.kind, test.code)
			var nodeErr *agentnode.NodeError
			if !errors.As(err, &nodeErr) || nodeErr.Details != nil || strings.Contains(err.Error(), "SENSITIVE") {
				t.Fatalf("unsafe NodeError=%+v", nodeErr)
			}
		})
	}
}

func TestLLMV2DoesNotClassifyTextModeBadRequestAsStructuredRejection(t *testing.T) {
	provider := &llmV2RecordingProvider{err: &modelprovider.ProviderError{StatusCode: http.StatusBadRequest}}
	_, err := NewLLMV2(provider, "model").Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"outputMode":"text"}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	assertNodeError(t, err, agentnode.ErrorKindInternal, "execution_failed")
}

func TestLLMV2RejectsMissingAndInvalidPrompt(t *testing.T) {
	node := NewLLMV2(modelprovider.NewMock(), "model")
	_, missing := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
	assertNodeError(t, missing, agentnode.ErrorKindInput, "missing_input")
	_, invalid := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {7}},
	})
	assertNodeError(t, invalid, agentnode.ErrorKindInput, "invalid_input")
	_, multiple := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"first", "second"}},
	})
	assertNodeError(t, multiple, agentnode.ErrorKindInput, "invalid_input")
}

func TestLLMV2HonorsPreCanceledContextBeforeProviderResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &llmV2RecordingProvider{response: modelprovider.Response{Text: "ignored"}}
	_, err := NewLLMV2(provider, "model").Execute(ctx, agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	assertNodeError(t, err, agentnode.ErrorKindCanceled, "run_canceled")
}

func TestLLMV2RejectsProviderSuccessAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := llmV2CancelingProvider{cancel: cancel}
	_, err := NewLLMV2(provider, "model").Execute(ctx, agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	assertNodeError(t, err, agentnode.ErrorKindCanceled, "run_canceled")
}

func TestLLMV2RejectsMissingProvider(t *testing.T) {
	_, err := NewLLMV2(nil, "model").Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"hello"}},
	})
	assertNodeError(t, err, agentnode.ErrorKindInternal, "execution_failed")
}

func assertPortKeys(t *testing.T, ports []agentnode.Port, want ...string) {
	t.Helper()
	got := make([]string, 0, len(ports))
	for _, port := range ports {
		got = append(got, port.Key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ports=%v want=%v", got, want)
	}
}
