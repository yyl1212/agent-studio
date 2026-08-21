package builtin

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

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
