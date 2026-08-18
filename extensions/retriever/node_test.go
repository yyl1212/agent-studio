package retriever

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"github.com/yyl1212/agent-studio/sdk/go/agenttest"
)

func TestNodeContract(t *testing.T) {
	inputKind := agentnode.ErrorKindInput
	agenttest.Run(t, agenttest.Contract{
		Node: Node{},
		ValidConfigs: []json.RawMessage{
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go Agent"}],"topK":1}`),
		},
		InvalidConfigs: []json.RawMessage{
			json.RawMessage(`{}`),
			json.RawMessage(`{"documents":[],"topK":1}`),
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"},{"id":" doc-1 ","text":"Agent"}],"topK":1}`),
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"---"}],"topK":1}`),
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":0}`),
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":101}`),
			json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":1,"unknown":true}`),
		},
		Executions: []agenttest.ExecutionCase{
			{
				Name: "deterministic retrieval",
				Request: agentnode.Request{
					Config: json.RawMessage(`{"documents":[{"id":"d1","text":"Go Agent"},{"id":"d2","text":"Rust"}],"topK":1}`),
					Inputs: map[string][]any{"query": {"GO agent"}},
				},
				WantOutputs: map[string]any{"matches": []Match{{ID: "d1", Text: "Go Agent", Score: 1}}},
			},
			{
				Name: "invalid query",
				Request: agentnode.Request{
					Config: json.RawMessage(`{"documents":[{"id":"d1","text":"Go"}],"topK":1}`),
					Inputs: map[string][]any{"query": {"---"}},
				},
				WantErrorKind: &inputKind,
			},
		},
	})
}

func TestDefinition(t *testing.T) {
	definition := (Node{}).Definition()
	if definition.Type != "extension.retriever" || definition.Version != "1.0.0" || definition.Title != "Retriever" || definition.Description != "使用本地 Jaccard 相似度检索配置文档" || definition.Category != "扩展" {
		t.Fatalf("definition = %+v", definition)
	}
	if len(definition.Capabilities) != 0 {
		t.Fatalf("capabilities = %v, want none", definition.Capabilities)
	}
	wantInputs := []agentnode.Port{{Key: "query", Title: "查询", Type: agentnode.DataTypeString, Required: true, Cardinality: agentnode.CardinalityOne}}
	wantOutputs := []agentnode.Port{{Key: "matches", Title: "匹配结果", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne}}
	if !reflect.DeepEqual(definition.Inputs, wantInputs) || !reflect.DeepEqual(definition.Outputs, wantOutputs) {
		t.Fatalf("inputs=%+v outputs=%+v", definition.Inputs, definition.Outputs)
	}
}

func TestDefinitionAndResolveReturnIndependentPorts(t *testing.T) {
	firstDefinition := (Node{}).Definition()
	firstDefinition.Inputs[0].Key = "mutated"
	firstDefinition.Outputs[0].Key = "mutated"
	secondDefinition := (Node{}).Definition()
	if secondDefinition.Inputs[0].Key != "query" || secondDefinition.Outputs[0].Key != "matches" {
		t.Fatalf("definition shares mutable ports: %+v", secondDefinition)
	}

	config := json.RawMessage(`{"documents":[{"id":"d1","text":"Go"}],"topK":1}`)
	firstPorts, err := (Node{}).Resolve(config)
	if err != nil {
		t.Fatal(err)
	}
	firstPorts.Inputs[0].Key = "mutated"
	firstPorts.Outputs[0].Key = "mutated"
	secondPorts, err := (Node{}).Resolve(config)
	if err != nil {
		t.Fatal(err)
	}
	if secondPorts.Inputs[0].Key != "query" || secondPorts.Outputs[0].Key != "matches" {
		t.Fatalf("resolved ports share mutable slices: %+v", secondPorts)
	}
}

func TestResolveConfigBoundaries(t *testing.T) {
	oneDocument := []map[string]string{{"id": "doc-1", "text": "Go"}}
	tests := []struct {
		name    string
		config  json.RawMessage
		wantErr bool
	}{
		{name: "one document", config: configJSON(t, oneDocument, 1)},
		{name: "one thousand documents", config: configJSON(t, documents(1000), 100)},
		{name: "id has 128 runes", config: configJSON(t, []map[string]string{{"id": strings.Repeat("界", 128), "text": "Go"}}, 1)},
		{name: "text has 65536 runes", config: configJSON(t, []map[string]string{{"id": "doc-1", "text": strings.Repeat("界", 65536)}}, 1)},
		{name: "topK one", config: configJSON(t, oneDocument, 1)},
		{name: "topK one hundred", config: configJSON(t, oneDocument, 100)},
		{name: "missing fields", config: json.RawMessage(`{}`), wantErr: true},
		{name: "no documents", config: configJSON(t, []map[string]string{}, 1), wantErr: true},
		{name: "too many documents", config: configJSON(t, documents(1001), 1), wantErr: true},
		{name: "blank id", config: configJSON(t, []map[string]string{{"id": " \t ", "text": "Go"}}, 1), wantErr: true},
		{name: "id has 129 runes", config: configJSON(t, []map[string]string{{"id": strings.Repeat("界", 129), "text": "Go"}}, 1), wantErr: true},
		{name: "duplicate trimmed id", config: configJSON(t, []map[string]string{{"id": "doc-1", "text": "Go"}, {"id": " doc-1 ", "text": "Agent"}}, 1), wantErr: true},
		{name: "blank text", config: configJSON(t, []map[string]string{{"id": "doc-1", "text": " \n "}}, 1), wantErr: true},
		{name: "text has 65537 runes", config: configJSON(t, []map[string]string{{"id": "doc-1", "text": strings.Repeat("界", 65537)}}, 1), wantErr: true},
		{name: "text has no tokens", config: configJSON(t, []map[string]string{{"id": "doc-1", "text": "---"}}, 1), wantErr: true},
		{name: "topK zero", config: configJSON(t, oneDocument, 0), wantErr: true},
		{name: "topK too large", config: configJSON(t, oneDocument, 101), wantErr: true},
		{name: "topK non integer", config: json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":1.5}`), wantErr: true},
		{name: "unknown field", config: json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":1,"unknown":true}`), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (Node{}).Resolve(test.config)
			if test.wantErr {
				assertNodeError(t, err, agentnode.ErrorKindConfig, "invalid_config")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecuteOrdersRoundsAndKeepsZeroScores(t *testing.T) {
	config := json.RawMessage(`{"documents":[{"id":"first","text":"Agent Go Rust"},{"id":"second","text":"AGENT go"},{"id":"third","text":"Java"}],"topK":3}`)
	result, err := (Node{}).Execute(context.Background(), agentnode.Request{
		Config: config,
		Inputs: map[string][]any{"query": {"agent GO"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{ID: "second", Text: "AGENT go", Score: 1},
		{ID: "first", Text: "Agent Go Rust", Score: 0.666667},
		{ID: "third", Text: "Java", Score: 0},
	}
	if got := result.Outputs["matches"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("matches=%#v want=%#v", got, want)
	}
}

func TestExecuteUsesUnicodeTokensAndStableTieOrder(t *testing.T) {
	config := json.RawMessage(`{"documents":[{"id":"a","text":"你好 AGENT"},{"id":"b","text":"你好 agent"}],"topK":100}`)
	result, err := (Node{}).Execute(context.Background(), agentnode.Request{
		Config: config,
		Inputs: map[string][]any{"query": {"你好 Agent"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{{ID: "a", Text: "你好 AGENT", Score: 1}, {ID: "b", Text: "你好 agent", Score: 1}}
	if !reflect.DeepEqual(result.Outputs["matches"], want) {
		t.Fatalf("matches=%#v", result.Outputs["matches"])
	}
}

func TestExecuteLimitsResultsAndPreservesDocumentValues(t *testing.T) {
	result, err := (Node{}).Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"documents":[{"id":" doc-1 ","text":" Go Agent "},{"id":"doc-2","text":"Go"}],"topK":1}`),
		Inputs: map[string][]any{"query": {"go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{{ID: "doc-2", Text: "Go", Score: 1}}
	if !reflect.DeepEqual(result.Outputs["matches"], want) {
		t.Fatalf("matches=%#v want=%#v", result.Outputs["matches"], want)
	}

	result, err = (Node{}).Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"documents":[{"id":" doc-1 ","text":" Go Agent "}],"topK":100}`),
		Inputs: map[string][]any{"query": {"go agent"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []Match{{ID: " doc-1 ", Text: " Go Agent ", Score: 1}}
	if !reflect.DeepEqual(result.Outputs["matches"], want) {
		t.Fatalf("matches=%#v want=%#v", result.Outputs["matches"], want)
	}
}

func TestExecuteIsByteDeterministicAndReturnsIndependentContainers(t *testing.T) {
	request := agentnode.Request{
		Config: json.RawMessage(`{"documents":[{"id":"d1","text":"Go Agent"},{"id":"d2","text":"Go"}],"topK":2}`),
		Inputs: map[string][]any{"query": {"go"}},
	}
	first, err := (Node{}).Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	first.Outputs["matches"].([]Match)[0].ID = "mutated"
	for iteration := 0; iteration < 20; iteration++ {
		next, err := (Node{}).Execute(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		nextJSON, err := json.Marshal(next)
		if err != nil {
			t.Fatal(err)
		}
		if string(nextJSON) != string(firstJSON) {
			t.Fatalf("iteration %d produced %s, want %s", iteration, nextJSON, firstJSON)
		}
	}
}

func TestExecuteRejectsInvalidQueryInputs(t *testing.T) {
	config := json.RawMessage(`{"documents":[{"id":"doc-1","text":"Go"}],"topK":1}`)
	tests := []struct {
		name   string
		inputs map[string][]any
	}{
		{name: "missing", inputs: map[string][]any{}},
		{name: "two values", inputs: map[string][]any{"query": {"go", "agent"}}},
		{name: "wrong type", inputs: map[string][]any{"query": {42}}},
		{name: "blank", inputs: map[string][]any{"query": {" \t "}}},
		{name: "punctuation", inputs: map[string][]any{"query": {"---"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (Node{}).Execute(context.Background(), agentnode.Request{Config: config, Inputs: test.inputs})
			assertNodeError(t, err, agentnode.ErrorKindInput, "invalid_query")
			if len(result.Outputs) != 0 || len(result.ActivePorts) != 0 {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExecuteHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (Node{}).Execute(ctx, agentnode.Request{})
	assertNodeError(t, err, agentnode.ErrorKindCanceled, "run_canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v does not wrap context.Canceled", err)
	}
	if len(result.Outputs) != 0 || len(result.ActivePorts) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func configJSON(t *testing.T, documentValues []map[string]string, topK any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"documents": documentValues, "topK": topK})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func documents(count int) []map[string]string {
	result := make([]map[string]string, count)
	for index := range count {
		result[index] = map[string]string{"id": fmt.Sprintf("doc-%d", index), "text": "Go"}
	}
	return result
}

func assertNodeError(t *testing.T, err error, kind agentnode.ErrorKind, code string) {
	t.Helper()
	var nodeErr *agentnode.NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Kind != kind || nodeErr.Code != code {
		t.Fatalf("error=%v kind=%q, want %s/%s", err, agentnode.KindOf(err), kind, code)
	}
}
