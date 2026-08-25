package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/modelprovider"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"github.com/yyl1212/agent-studio/sdk/go/agenttest"
)

type contractProvider struct{}

func (contractProvider) Complete(ctx context.Context, request modelprovider.Request) (modelprovider.Response, error) {
	switch request.Prompt {
	case "wait":
		<-ctx.Done()
		return modelprovider.Response{}, ctx.Err()
	case "timeout":
		return modelprovider.Response{}, context.DeadlineExceeded
	default:
		return modelprovider.Response{Text: "answer", Usage: map[string]int{"totalTokens": 2}}, nil
	}
}

func TestOfficialBuiltinExecutionSafetyMatrix(t *testing.T) {
	tests := []struct {
		node agentnode.Node
		want agentnode.ExecutionSafety
	}{
		{node: NewStart(), want: agentnode.ExecutionSafetyPure},
		{node: NewTemplate(), want: agentnode.ExecutionSafetyPure},
		{node: NewCondition(), want: agentnode.ExecutionSafetyPure},
		{node: NewEnd(), want: agentnode.ExecutionSafetyPure},
		{node: NewCode(CodeOptions{}), want: agentnode.ExecutionSafetyPure},
		{node: NewLLM(contractProvider{}, "contract-model"), want: agentnode.ExecutionSafetyReadOnly},
		{node: NewLLMV2(contractProvider{}, "contract-model"), want: agentnode.ExecutionSafetyReadOnly},
		{node: NewHTTP(HTTPOptions{}), want: agentnode.ExecutionSafetySideEffect},
	}

	for _, test := range tests {
		definition := test.node.Definition()
		t.Run(definition.Type+"@"+definition.Version, func(t *testing.T) {
			if got := definition.ExecutionSafety; got != test.want {
				t.Fatalf("execution safety=%q, want %q", got, test.want)
			}
		})
	}
}

func TestCoreNodeContracts(t *testing.T) {
	inputKind := agentnode.ErrorKindInput
	internalKind := agentnode.ErrorKindInternal
	tests := []struct {
		name     string
		contract agenttest.Contract
	}{
		{
			name: "start",
			contract: agenttest.Contract{
				Node: NewStart(),
				ValidConfigs: []json.RawMessage{
					json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
				},
				InvalidConfigs: []json.RawMessage{
					json.RawMessage(`{"fields":[{"key":"bad-key","label":"主题","type":"text"}]}`),
				},
				Executions: []agenttest.ExecutionCase{{
					Name: "emits run input",
					Request: agentnode.Request{
						Config:   json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
						RunInput: map[string]any{"topic": "Agent"},
					},
					WantOutputs: map[string]any{"topic": "Agent"},
				}, {
					Name: "classifies missing run input",
					Request: agentnode.Request{
						Config:   json.RawMessage(`{"fields":[{"key":"topic","label":"主题","type":"text","required":true}]}`),
						RunInput: map[string]any{},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
		{
			name: "template",
			contract: agenttest.Contract{
				Node:           NewTemplate(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{"template":"你好，{{name}}"}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"template":"你好，{{ name }}"}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "renders variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"你好，{{name}}"}`),
						Inputs: map[string][]any{"name": {"Codex"}},
					},
					WantOutputs: map[string]any{"text": "你好，Codex"},
				}, {
					Name: "classifies missing variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"你好，{{name}}"}`),
						Inputs: map[string][]any{},
					},
					WantErrorKind: &inputKind,
				}, {
					Name: "classifies non JSON variable",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"template":"{{value}}"}`),
						Inputs: map[string][]any{"value": {make(chan int)}},
					},
					WantErrorKind: &internalKind,
				}},
			},
		},
		{
			name: "condition",
			contract: agenttest.Contract{
				Node:           NewCondition(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{"operator":"equals","compareValue":"yes"}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"operator":"unknown"}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "activates true branch",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"operator":"equals","compareValue":"yes"}`),
						Inputs: map[string][]any{"value": {"yes"}},
					},
					WantOutputs: map[string]any{"true": "yes"},
				}, {
					Name: "classifies incompatible comparison",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"operator":"greaterThan","compareValue":2}`),
						Inputs: map[string][]any{"value": {"three"}},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
		{
			name: "end",
			contract: agenttest.Contract{
				Node:           NewEnd(),
				ValidConfigs:   []json.RawMessage{json.RawMessage(`{}`)},
				InvalidConfigs: []json.RawMessage{json.RawMessage(`{"unknown":true}`)},
				Executions: []agenttest.ExecutionCase{{
					Name: "returns result",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{"result": {"done"}},
					},
					WantOutputs: map[string]any{"result": "done"},
				}, {
					Name: "classifies missing result",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{},
					},
					WantErrorKind: &inputKind,
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agenttest.Run(t, test.contract)
		})
	}
}

func TestIntegrationNodeContracts(t *testing.T) {
	temporaryKind := agentnode.ErrorKindTemporary
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/wait", "/slow":
			<-request.Context().Done()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Date", "Mon, 02 Jan 2006 15:04:05 GMT")
		writer.Header().Set("Content-Length", "11")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	t.Run("llm", func(t *testing.T) {
		agenttest.Run(t, agenttest.Contract{
			Node:           NewLLM(contractProvider{}, "contract-model"),
			ValidConfigs:   []json.RawMessage{json.RawMessage(`{}`)},
			InvalidConfigs: []json.RawMessage{json.RawMessage(`{"temperature":3}`)},
			Executions: []agenttest.ExecutionCase{
				{
					Name: "generates text",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{"prompt": {"hello"}},
					},
					WantOutputs: map[string]any{"text": "answer", "usage": map[string]int{"totalTokens": 2}},
				},
				{
					Name: "classifies upstream timeout",
					Request: agentnode.Request{
						Config: json.RawMessage(`{}`),
						Inputs: map[string][]any{"prompt": {"timeout"}},
					},
					WantErrorKind: &temporaryKind,
				},
			},
			Cancellation: &agenttest.CancellationCase{Request: agentnode.Request{
				Config: json.RawMessage(`{}`),
				Inputs: map[string][]any{"prompt": {"wait"}},
			}},
		})
	})

	t.Run("llm v2 text", func(t *testing.T) {
		agenttest.Run(t, agenttest.Contract{
			Node:           NewLLMV2(contractProvider{}, "contract-model"),
			ValidConfigs:   []json.RawMessage{json.RawMessage(`{"outputMode":"text"}`)},
			InvalidConfigs: []json.RawMessage{json.RawMessage(`{"outputMode":"structured","fields":[]}`)},
			Executions: []agenttest.ExecutionCase{
				{
					Name: "generates text",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"outputMode":"text"}`),
						Inputs: map[string][]any{"prompt": {"hello"}},
					},
					WantOutputs: map[string]any{"text": "answer", "usage": map[string]int{"totalTokens": 2}},
				},
				{
					Name: "classifies upstream timeout",
					Request: agentnode.Request{
						Config: json.RawMessage(`{"outputMode":"text"}`),
						Inputs: map[string][]any{"prompt": {"timeout"}},
					},
					WantErrorKind: &temporaryKind,
				},
			},
			Cancellation: &agenttest.CancellationCase{Request: agentnode.Request{
				Config: json.RawMessage(`{"outputMode":"text"}`),
				Inputs: map[string][]any{"prompt": {"wait"}},
			}},
		})
	})

	t.Run("http", func(t *testing.T) {
		successConfig := json.RawMessage(fmt.Sprintf(`{"method":"GET","url":%q,"headers":[]}`, server.URL+"/success"))
		timeoutConfig := json.RawMessage(fmt.Sprintf(`{"method":"GET","url":%q,"headers":[],"timeoutMs":5}`, server.URL+"/slow"))
		cancelConfig := json.RawMessage(fmt.Sprintf(`{"method":"GET","url":%q,"headers":[],"timeoutMs":30000}`, server.URL+"/wait"))
		agenttest.Run(t, agenttest.Contract{
			Node:         NewHTTP(HTTPOptions{AllowPrivateNetwork: true}),
			ValidConfigs: []json.RawMessage{successConfig},
			InvalidConfigs: []json.RawMessage{json.RawMessage(fmt.Sprintf(
				`{"method":"GET","url":%q,"headers":[{"name":"Authorization","valueSource":"literal","value":"top-secret"}]}`,
				server.URL,
			))},
			Executions: []agenttest.ExecutionCase{
				{
					Name:    "returns response",
					Request: agentnode.Request{Config: successConfig},
					WantOutputs: map[string]any{
						"status": float64(http.StatusOK),
						"headers": map[string][]string{
							"Content-Length": {"11"},
							"Content-Type":   {"application/json"},
							"Date":           {"Mon, 02 Jan 2006 15:04:05 GMT"},
						},
						"body": map[string]any{"ok": true},
					},
				},
				{
					Name:          "classifies upstream timeout",
					Request:       agentnode.Request{Config: timeoutConfig},
					WantErrorKind: &temporaryKind,
				},
			},
			Cancellation: &agenttest.CancellationCase{Request: agentnode.Request{Config: cancelConfig}},
		})
	})

	t.Run("code", func(t *testing.T) {
		node := NewCode(CodeOptions{MaxSteps: 1 << 62, Timeout: time.Second})
		agenttest.Run(t, agenttest.Contract{
			Node:           node,
			ValidConfigs:   []json.RawMessage{json.RawMessage(`{"source":"def main(input):\n  return \"ok\""}`)},
			InvalidConfigs: []json.RawMessage{json.RawMessage(fmt.Sprintf(`{"source":%q}`, strings.Repeat("x", maxCodeSourceBytes+1)))},
			Executions: []agenttest.ExecutionCase{{
				Name:        "executes Starlark",
				Request:     agentnode.Request{Config: json.RawMessage(`{"source":"def main(input):\n  return \"ok\""}`)},
				WantOutputs: map[string]any{"result": "ok"},
			}},
			Cancellation: &agenttest.CancellationCase{Request: agentnode.Request{
				Config: json.RawMessage(`{"source":"def main(input):\n  for i in range(1000000000):\n    pass\n  return None"}`),
			}},
		})
	})

	t.Run("code timeout", func(t *testing.T) {
		agenttest.Run(t, agenttest.Contract{
			Node: NewCode(CodeOptions{MaxSteps: 1 << 62, Timeout: time.Millisecond}),
			Executions: []agenttest.ExecutionCase{{
				Name: "classifies execution timeout",
				Request: agentnode.Request{Config: json.RawMessage(
					`{"source":"def main(input):\n  for i in range(1000000000):\n    pass\n  return None"}`,
				)},
				WantErrorKind: &temporaryKind,
			}},
		})
	})
}

func TestCoreNodesClassifyPreCanceledContext(t *testing.T) {
	tests := []struct {
		name    string
		node    agentnode.Node
		request agentnode.Request
	}{
		{name: "start", node: NewStart(), request: agentnode.Request{Config: json.RawMessage(`{"fields":[]}`)}},
		{name: "template", node: NewTemplate(), request: agentnode.Request{Config: json.RawMessage(`{"template":"static"}`)}},
		{name: "condition", node: NewCondition(), request: agentnode.Request{
			Config: json.RawMessage(`{"operator":"equals","compareValue":"yes"}`),
			Inputs: map[string][]any{"value": {"yes"}},
		}},
		{name: "end", node: NewEnd(), request: agentnode.Request{
			Config: json.RawMessage(`{}`),
			Inputs: map[string][]any{"result": {"done"}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := test.node.Execute(ctx, test.request)
			assertNodeError(t, err, agentnode.ErrorKindCanceled, "run_canceled")
		})
	}
}

func TestIntegrationNodeCapabilities(t *testing.T) {
	tests := []struct {
		name string
		node agentnode.Node
		want []agentnode.Capability
	}{
		{name: "llm", node: NewLLM(contractProvider{}, "model"), want: []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}},
		{name: "llm v2", node: NewLLMV2(contractProvider{}, "model"), want: []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}},
		{name: "http", node: NewHTTP(HTTPOptions{}), want: []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}},
		{name: "code", node: NewCode(CodeOptions{}), want: []agentnode.Capability{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.node.Definition().Capabilities; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("capabilities = %v, want %v", got, test.want)
			}
		})
	}
}

func TestIntegrationNodeErrorCodes(t *testing.T) {
	llm := NewLLM(contractProvider{}, "model")
	_, configErr := llm.Resolve(json.RawMessage(`{"temperature":3}`))
	assertNodeError(t, configErr, agentnode.ErrorKindConfig, "invalid_config")
	_, inputErr := llm.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
	assertNodeError(t, inputErr, agentnode.ErrorKindInput, "missing_input")
	_, timeoutErr := llm.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{}`),
		Inputs: map[string][]any{"prompt": {"timeout"}},
	})
	assertNodeError(t, timeoutErr, agentnode.ErrorKindTemporary, "upstream_timeout")

	llmV2 := NewLLMV2(contractProvider{}, "model")
	_, v2ConfigErr := llmV2.Resolve(json.RawMessage(`{"outputMode":"structured","fields":[]}`))
	assertNodeError(t, v2ConfigErr, agentnode.ErrorKindConfig, "invalid_config")
	_, v2InputErr := llmV2.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{}`)})
	assertNodeError(t, v2InputErr, agentnode.ErrorKindInput, "missing_input")

	code := NewCode(CodeOptions{MaxSteps: 1 << 62, Timeout: time.Millisecond})
	_, codeTimeoutErr := code.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(
		`{"source":"def main(input):\n  for i in range(1000000000):\n    pass\n  return None"}`,
	)})
	assertNodeError(t, codeTimeoutErr, agentnode.ErrorKindTemporary, "upstream_timeout")
}

func assertNodeError(t *testing.T, err error, kind agentnode.ErrorKind, code string) {
	t.Helper()
	var nodeErr *agentnode.NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatalf("error %v is not a NodeError", err)
	}
	if nodeErr.Kind != kind || nodeErr.Code != code {
		t.Fatalf("NodeError = %s/%s, want %s/%s", nodeErr.Kind, nodeErr.Code, kind, code)
	}
}

func TestHTTPSecretDoesNotEnterOutputs(t *testing.T) {
	const secret = "Bearer top-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != secret {
			t.Errorf("Authorization = %q, want injected secret", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Echo-Authorization", secret)
		_, _ = writer.Write([]byte(fmt.Sprintf(`{"echo":%q}`, secret)))
	}))
	defer server.Close()
	node := NewHTTP(HTTPOptions{
		AllowPrivateNetwork: true,
		LookupEnv: func(name string) (string, bool) {
			return secret, name == "UPSTREAM_AUTH"
		},
	})
	config := json.RawMessage(fmt.Sprintf(
		`{"method":"GET","url":%q,"headers":[{"name":"Authorization","valueSource":"env","envName":"UPSTREAM_AUTH"}]}`,
		server.URL,
	))
	result, err := node.Execute(context.Background(), agentnode.Request{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "top-secret") {
		t.Fatalf("secret leaked into outputs: %s", encoded)
	}
	if !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("echoed secret was not redacted: %s", encoded)
	}
}

func TestHTTPTimeoutCoversDNSResolution(t *testing.T) {
	lookup := func(ctx context.Context, _, _ string) ([]net.IP, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil, errors.New("DNS lookup did not receive timeout context")
		}
	}
	node := NewHTTP(HTTPOptions{LookupIP: lookup})
	started := time.Now()
	_, err := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(
		`{"method":"GET","url":"https://slow-dns.example","headers":[],"timeoutMs":5}`,
	)})
	assertNodeError(t, err, agentnode.ErrorKindTemporary, "upstream_timeout")
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("DNS timeout took %s, want <= 50ms", elapsed)
	}
}

func TestHTTPClassifiesDNSFailureAsInternal(t *testing.T) {
	lookup := func(context.Context, string, string) ([]net.IP, error) {
		return nil, errors.New("resolver unavailable")
	}
	node := NewHTTP(HTTPOptions{LookupIP: lookup})
	_, err := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(
		`{"method":"GET","url":"https://dns-failure.example","headers":[]}`,
	)})
	assertNodeError(t, err, agentnode.ErrorKindInternal, "execution_failed")
}

func TestBuiltinSchemasDoNotExposePlaintextSecretFields(t *testing.T) {
	nodes := []agentnode.Node{
		NewStart(), NewTemplate(), NewCondition(), NewEnd(),
		NewLLM(contractProvider{}, "model"), NewLLMV2(contractProvider{}, "model"), NewHTTP(HTTPOptions{}), NewCode(CodeOptions{}),
	}
	for _, node := range nodes {
		var schema map[string]any
		if err := json.Unmarshal(node.Definition().ConfigSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if field, found := findPlaintextSecretField(schema); found {
			t.Errorf("node %s exposes plaintext secret field %q", node.Definition().Type, field)
		}
	}
}

func findPlaintextSecretField(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		for key := range properties {
			normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
			for _, marker := range []string{"apikey", "secret", "password", "cookie", "authorization"} {
				if strings.Contains(normalized, marker) {
					return key, true
				}
			}
			if normalized == "token" || strings.Contains(normalized, "accesstoken") ||
				strings.Contains(normalized, "refreshtoken") || strings.Contains(normalized, "authtoken") ||
				strings.Contains(normalized, "bearertoken") {
				return key, true
			}
		}
	}
	for _, child := range object {
		switch typed := child.(type) {
		case map[string]any:
			if field, found := findPlaintextSecretField(typed); found {
				return field, true
			}
		case []any:
			for _, item := range typed {
				if field, found := findPlaintextSecretField(item); found {
					return field, true
				}
			}
		}
	}
	return "", false
}
