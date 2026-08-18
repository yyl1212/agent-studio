package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
	"github.com/yyl1212/agent-studio/sdk/go/agenttest"
)

func TestParseConfigDefaultsAndValidatesTimeout(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantPath  string
		wantDelay int
		wantErr   bool
	}{
		{name: "default timeout", raw: `{"path":"hooks/run"}`, wantPath: "hooks/run", wantDelay: 5000},
		{name: "trimmed path", raw: `{"path":"  hooks/run  "}`, wantPath: "hooks/run", wantDelay: 5000},
		{name: "minimum timeout", raw: `{"path":"hooks/run","timeoutMs":1}`, wantPath: "hooks/run", wantDelay: 1},
		{name: "maximum timeout", raw: `{"path":"hooks/run","timeoutMs":30000}`, wantPath: "hooks/run", wantDelay: 30000},
		{name: "missing path", raw: `{}`, wantErr: true},
		{name: "zero explicit timeout", raw: `{"path":"hooks/run","timeoutMs":0}`, wantErr: true},
		{name: "negative timeout", raw: `{"path":"hooks/run","timeoutMs":-1}`, wantErr: true},
		{name: "large timeout", raw: `{"path":"hooks/run","timeoutMs":30001}`, wantErr: true},
		{name: "fractional timeout", raw: `{"path":"hooks/run","timeoutMs":1.5}`, wantErr: true},
		{name: "unknown field", raw: `{"path":"hooks/run","url":"https://evil.example"}`, wantErr: true},
		{name: "malformed JSON", raw: `{"path":`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseConfig(json.RawMessage(test.raw))
			if test.wantErr {
				assertNodeError(t, err, agentnode.ErrorKindConfig, "invalid_config")
				return
			}
			if err != nil || got.Path != test.wantPath || got.TimeoutMS != test.wantDelay {
				t.Fatalf("config=%+v err=%v", got, err)
			}
		})
	}
}

func TestParseConfigSanitizesDecoderErrors(t *testing.T) {
	const secret = "top-secret-token"
	_, err := parseConfig(json.RawMessage(`{"path":"hooks/run","` + secret + `":true}`))
	assertNodeError(t, err, agentnode.ErrorKindConfig, "invalid_config")
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), secret) || strings.Contains(current.Error(), "hooks/run") {
			t.Fatalf("error chain leaked dynamic config: %v", current)
		}
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error=%v does not wrap ErrInvalidConfig", err)
	}
}

func TestValidateRelativePath(t *testing.T) {
	valid := map[string]string{
		"hooks/run":             "hooks/run",
		"  hooks/run  ":         "hooks/run",
		"v1/任务":                 "v1/任务",
		"dot.segment/file-name": "dot.segment/file-name",
	}
	for raw, want := range valid {
		got, err := validateRelativePath(raw)
		if err != nil || got != want {
			t.Errorf("path %q got=%q err=%v, want=%q", raw, got, err, want)
		}
	}

	deeplyEncoded := "../admin"
	for range maxUnescapePasses + 1 {
		deeplyEncoded = url.PathEscape(deeplyEncoded)
	}
	invalid := []string{
		"", "   ", "/absolute", `\\absolute`, "../admin", "a/../admin",
		"https://evil.example/hook", "https%3A%2F%2Fevil.example%2Fhook", "//evil.example/hook",
		"hook?token=x", "hook#fragment", "%2e%2e/admin", "%252e%252e/admin",
		"%2Fadmin", "hook%3Ftoken=x", "hook%23fragment", "%5cadmin", "%zz",
		deeplyEncoded,
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if _, err := validateRelativePath(raw); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("invalid path %q error=%v", raw, err)
			}
		})
	}
}

func TestParseBaseURLAndJoin(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{name: "HTTPS base path", base: "https://upstream.example/root/api", path: "hooks/run", want: "https://upstream.example/root/api/hooks/run"},
		{name: "HTTP host", base: "http://localhost:8080", path: "hook", want: "http://localhost:8080/hook"},
		{name: "IPv6 host", base: "http://[::1]:8080/base", path: "hook", want: "http://[::1]:8080/base/hook"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, err := parseBaseURL(test.base)
			if err != nil {
				t.Fatal(err)
			}
			target := joinTarget(base, test.path)
			if got := target.String(); got != test.want {
				t.Fatalf("target=%q want=%q", got, test.want)
			}
			if _, err := url.Parse(target.String()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestParseBaseURLRejectsUnsafeValuesWithoutLeakingThem(t *testing.T) {
	invalid := []string{
		"", "   ", "ftp://example.com", "https:///missing", "https:opaque",
		"https://user:pass@example.com", "https://example.com?x=1", "https://example.com?", "https://example.com#x",
		"https://example.com/\nsecret",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			_, err := parseBaseURL(raw)
			if !errors.Is(err, ErrMissingConfiguration) {
				t.Fatalf("base %q error=%v", raw, err)
			}
			for current := err; current != nil; current = errors.Unwrap(current) {
				if raw != "" && strings.Contains(current.Error(), raw) {
					t.Fatalf("error chain leaked base URL %q: %v", raw, current)
				}
			}
		})
	}
}

func TestDefinitionAndResolveDoNotReadEnvironment(t *testing.T) {
	lookups := 0
	node := New(Options{LookupEnv: func(string) (string, bool) {
		lookups++
		return "", false
	}})
	definition := node.Definition()
	if definition.Type != "extension.webhook" || definition.Version != "1.0.0" || definition.Title != "Webhook" || definition.Description != "向运维配置的基地址发送受约束的 JSON POST 请求" || definition.Category != "扩展" {
		t.Fatalf("definition=%+v", definition)
	}
	wantInputs := []agentnode.Port{{Key: "body", Title: "请求体", Type: agentnode.DataTypeJSON, Required: true, Cardinality: agentnode.CardinalityOne}}
	wantOutputs := []agentnode.Port{
		{Key: "status", Title: "状态码", Type: agentnode.DataTypeNumber, Cardinality: agentnode.CardinalityOne},
		{Key: "body", Title: "响应体", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
	}
	wantCapabilities := []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets}
	if !reflect.DeepEqual(definition.Inputs, wantInputs) || !reflect.DeepEqual(definition.Outputs, wantOutputs) || !reflect.DeepEqual(definition.Capabilities, wantCapabilities) {
		t.Fatalf("definition=%+v", definition)
	}
	ports, err := node.Resolve(json.RawMessage(`{"path":"hooks/run"}`))
	if err != nil {
		t.Fatal(err)
	}
	if lookups != 0 {
		t.Fatalf("Resolve performed %d environment lookups", lookups)
	}
	if !reflect.DeepEqual(ports.Inputs, definition.Inputs) || !reflect.DeepEqual(ports.Outputs, definition.Outputs) {
		t.Fatalf("ports=%+v definition=%+v", ports, definition)
	}
}

func TestDefinitionAndResolveReturnIndependentContainers(t *testing.T) {
	node := New(Options{})
	first := node.Definition()
	first.Inputs[0].Key = "mutated"
	first.Outputs[0].Key = "mutated"
	first.Capabilities[0] = agentnode.CapabilityFilesystemWrite
	second := node.Definition()
	if second.Inputs[0].Key != "body" || second.Outputs[0].Key != "status" || second.Capabilities[0] != agentnode.CapabilityNetwork {
		t.Fatalf("definition shares containers: %+v", second)
	}
	config := json.RawMessage(`{"path":"hooks/run"}`)
	firstPorts, err := node.Resolve(config)
	if err != nil {
		t.Fatal(err)
	}
	firstPorts.Inputs[0].Key = "mutated"
	secondPorts, err := node.Resolve(config)
	if err != nil {
		t.Fatal(err)
	}
	if secondPorts.Inputs[0].Key != "body" {
		t.Fatalf("Resolve shares containers: %+v", secondPorts)
	}
}

func TestRegisterAddsWebhookNode(t *testing.T) {
	registrar := &captureRegistrar{}
	if err := Register(registrar); err != nil {
		t.Fatal(err)
	}
	if registrar.node == nil || registrar.node.Definition().Type != "extension.webhook" {
		t.Fatalf("node=%v", registrar.node)
	}
}

func TestNodeContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
	inputKind := agentnode.ErrorKindInput
	agenttest.Run(t, agenttest.Contract{
		Node: node,
		ValidConfigs: []json.RawMessage{
			json.RawMessage(`{"path":"hooks/run"}`),
			json.RawMessage(`{"path":"hooks/run","timeoutMs":30000}`),
		},
		InvalidConfigs: []json.RawMessage{
			json.RawMessage(`{}`),
			json.RawMessage(`{"path":"../admin"}`),
			json.RawMessage(`{"path":"hooks/run","timeoutMs":30001}`),
			json.RawMessage(`{"path":"hooks/run","headers":[]}`),
		},
		Executions: []agenttest.ExecutionCase{
			{
				Name: "post JSON",
				Request: agentnode.Request{
					Config: json.RawMessage(`{"path":"hooks/run"}`),
					Inputs: map[string][]any{"body": {map[string]any{"message": "hello"}}},
				},
				WantOutputs: map[string]any{"status": float64(http.StatusOK), "body": map[string]any{"ok": true}},
			},
			{
				Name:          "missing body",
				Request:       agentnode.Request{Config: json.RawMessage(`{"path":"hooks/run"}`)},
				WantErrorKind: &inputKind,
			},
		},
	})
}

func TestExecutePostsJSONWithOptionalBearerToken(t *testing.T) {
	const token = "stage-b-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/base/hooks/run" {
			t.Errorf("method=%s path=%s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type=%q", got)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization=%q", got)
		}
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"message":"hello"}` {
			t.Errorf("body=%s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Set-Cookie", "secret-cookie")
		_, _ = io.WriteString(writer, `{"accepted":true}`)
	}))
	defer server.Close()

	node := New(Options{LookupEnv: envLookup(map[string]string{
		webhookURLEnv:   server.URL + "/base",
		webhookTokenEnv: token,
	})})
	result, err := node.Execute(context.Background(), agentnode.Request{
		Config: json.RawMessage(`{"path":"hooks/run"}`),
		Inputs: map[string][]any{"body": {map[string]any{"message": "hello"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"status": float64(http.StatusOK), "body": map[string]any{"accepted": true}}
	if !reflect.DeepEqual(result.Outputs, want) {
		t.Fatalf("outputs=%#v want=%#v", result.Outputs, want)
	}
	if _, exists := result.Outputs["headers"]; exists {
		t.Fatalf("response headers leaked: %#v", result.Outputs)
	}
}

func TestExecuteOmitsAuthorizationWhenTokenIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if value, exists := request.Header["Authorization"]; exists || len(value) > 0 {
			t.Errorf("authorization=%v exists=%v", value, exists)
		}
		_, _ = io.WriteString(writer, `null`)
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL, webhookTokenEnv: ""})})
	_, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMapsHTTPStatusesAndBodies(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantBody     any
		wantKind     agentnode.ErrorKind
		wantCode     string
		wantSentinel error
	}{
		{name: "JSON success", status: 200, body: `{"ok":true}`, wantBody: map[string]any{"ok": true}},
		{name: "blank success", status: 200, body: " \n ", wantBody: nil},
		{name: "no content", status: 204, body: "", wantBody: nil},
		{name: "bad request", status: 400, body: `{"secret":"must-not-leak"}`, wantKind: agentnode.ErrorKindInput, wantCode: "webhook_rejected", wantSentinel: ErrWebhookRejected},
		{name: "not found", status: 404, body: `not found`, wantKind: agentnode.ErrorKindInput, wantCode: "webhook_rejected", wantSentinel: ErrWebhookRejected},
		{name: "request timeout", status: 408, body: `timeout`, wantKind: agentnode.ErrorKindTemporary, wantCode: "upstream_failed", wantSentinel: ErrUpstreamFailed},
		{name: "rate limited", status: 429, body: `limited`, wantKind: agentnode.ErrorKindTemporary, wantCode: "upstream_failed", wantSentinel: ErrUpstreamFailed},
		{name: "server error", status: 500, body: `failed`, wantKind: agentnode.ErrorKindTemporary, wantCode: "upstream_failed", wantSentinel: ErrUpstreamFailed},
		{name: "redirect without location", status: 302, body: `redirect`, wantKind: agentnode.ErrorKindTemporary, wantCode: "upstream_failed", wantSentinel: ErrUpstreamFailed},
		{name: "invalid JSON success", status: 200, body: `{bad`, wantKind: agentnode.ErrorKindTemporary, wantCode: "upstream_failed", wantSentinel: ErrInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
			result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"status": float64(test.status), "body": test.wantBody}
				if !reflect.DeepEqual(result.Outputs, want) {
					t.Fatalf("outputs=%#v want=%#v", result.Outputs, want)
				}
				return
			}
			assertNodeError(t, err, test.wantKind, test.wantCode)
			if !errors.Is(err, test.wantSentinel) {
				t.Fatalf("error=%v does not wrap %v", err, test.wantSentinel)
			}
			assertEmptyResult(t, result)
		})
	}
}

func TestExecuteBlocksRedirectBeforeForwardingToken(t *testing.T) {
	const token = "redirect-secret"
	var targetReached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		targetReached.Store(true)
		_, _ = io.WriteString(writer, `null`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer source.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: source.URL, webhookTokenEnv: token})})
	result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
	assertNodeError(t, err, agentnode.ErrorKindTemporary, "upstream_failed")
	if !errors.Is(err, ErrRedirectBlocked) || !errors.Is(err, ErrUpstreamFailed) {
		t.Fatalf("error=%v", err)
	}
	if targetReached.Load() {
		t.Fatal("redirect target received the request")
	}
	assertEmptyResult(t, result)
}

func TestExecuteValidatesInputBeforeNetwork(t *testing.T) {
	var reached atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		reached.Store(true)
		_, _ = io.WriteString(writer, `null`)
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
	tests := []struct {
		name   string
		inputs map[string][]any
	}{
		{name: "missing", inputs: map[string][]any{}},
		{name: "multiple", inputs: map[string][]any{"body": {map[string]any{}, map[string]any{}}}},
		{name: "not JSON encodable", inputs: map[string][]any{"body": {make(chan int)}}},
		{name: "too large", inputs: map[string][]any{"body": {jsonStringOfEncodedSize(maxBodyBytes + 1)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reached.Store(false)
			result, err := node.Execute(context.Background(), agentnode.Request{Config: json.RawMessage(`{"path":"hook"}`), Inputs: test.inputs})
			assertNodeError(t, err, agentnode.ErrorKindInput, "invalid_body")
			if !errors.Is(err, ErrInvalidBody) || reached.Load() {
				t.Fatalf("error=%v reached=%v", err, reached.Load())
			}
			assertEmptyResult(t, result)
		})
	}
}

func TestRequestBodySizeBoundary(t *testing.T) {
	for _, size := range []int{maxBodyBytes, maxBodyBytes + 1} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			var reached atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				reached.Store(true)
				body, _ := io.ReadAll(request.Body)
				if len(body) != size {
					t.Errorf("body size=%d want=%d", len(body), size)
				}
				_, _ = io.WriteString(writer, `null`)
			}))
			defer server.Close()
			node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
			result, err := node.Execute(context.Background(), validRequest(jsonStringOfEncodedSize(size)))
			if size == maxBodyBytes {
				if err != nil || !reached.Load() {
					t.Fatalf("result=%+v err=%v reached=%v", result, err, reached.Load())
				}
				return
			}
			assertNodeError(t, err, agentnode.ErrorKindInput, "invalid_body")
			if !errors.Is(err, ErrInvalidBody) || reached.Load() {
				t.Fatalf("error=%v reached=%v", err, reached.Load())
			}
		})
	}
}

func TestResponseBodySizeBoundary(t *testing.T) {
	for _, size := range []int{maxBodyBytes, maxBodyBytes + 1} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, `"`+strings.Repeat("x", size-2)+`"`)
			}))
			defer server.Close()
			node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
			result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
			if size == maxBodyBytes {
				if err != nil || len(result.Outputs["body"].(string)) != size-2 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				return
			}
			assertNodeError(t, err, agentnode.ErrorKindTemporary, "upstream_failed")
			if !errors.Is(err, ErrResponseTooLarge) || !errors.Is(err, ErrUpstreamFailed) {
				t.Fatalf("error=%v", err)
			}
			assertEmptyResult(t, result)
		})
	}
}

func TestExecuteClassifiesMissingBaseConfiguration(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com"} {
		node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: raw})})
		result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
		assertNodeError(t, err, agentnode.ErrorKindInternal, "missing_webhook_configuration")
		if !errors.Is(err, ErrMissingConfiguration) {
			t.Fatalf("error=%v", err)
		}
		assertEmptyResult(t, result)
	}
}

func TestExecuteHonorsCallerCancellation(t *testing.T) {
	node := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := node.Execute(ctx, agentnode.Request{})
	assertNodeError(t, err, agentnode.ErrorKindCanceled, "run_canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	assertEmptyResult(t, result)
}

func TestExecuteCancelsInFlightRequest(t *testing.T) {
	reached := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(reached)
		<-request.Context().Done()
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
	ctx, cancel := context.WithCancel(context.Background())
	resultChannel := make(chan struct {
		result agentnode.Result
		err    error
	}, 1)
	go func() {
		result, err := node.Execute(ctx, validRequest(map[string]any{"ok": true}))
		resultChannel <- struct {
			result agentnode.Result
			err    error
		}{result: result, err: err}
	}()
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatal("upstream was not reached")
	}
	cancel()
	select {
	case outcome := <-resultChannel:
		assertNodeError(t, outcome.err, agentnode.ErrorKindCanceled, "run_canceled")
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("error=%v", outcome.err)
		}
		assertEmptyResult(t, outcome.result)
	case <-time.After(time.Second):
		t.Fatal("Execute did not return after cancellation")
	}
}

func TestExecuteMapsNodeTimeoutToTemporaryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL})})
	request := validRequest(map[string]any{"ok": true})
	request.Config = json.RawMessage(`{"path":"hook","timeoutMs":20}`)
	result, err := node.Execute(context.Background(), request)
	assertNodeError(t, err, agentnode.ErrorKindTemporary, "upstream_failed")
	if !errors.Is(err, ErrUpstreamFailed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	assertEmptyResult(t, result)
}

func TestExecuteDropsUnsafeTransportError(t *testing.T) {
	const (
		baseURL = "https://private-upstream.example/base"
		token   = "transport-secret-token"
		body    = "raw-secret-body"
	)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed URL %s token %s body %s", request.URL.String(), request.Header.Get("Authorization"), body)
	})}
	node := New(Options{Client: client, LookupEnv: envLookup(map[string]string{webhookURLEnv: baseURL, webhookTokenEnv: token})})
	result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
	assertNodeError(t, err, agentnode.ErrorKindTemporary, "upstream_failed")
	if !errors.Is(err, ErrUpstreamFailed) {
		t.Fatalf("error=%v", err)
	}
	assertSafeErrorChain(t, err, baseURL, token, "Bearer", body)
	assertEmptyResult(t, result)
}

func TestSuccessfulResponseRecursivelyRedactsTokenOccurrences(t *testing.T) {
	const token = "top-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Authorization", "Bearer "+token)
		_, _ = io.WriteString(writer, `{"plain":"top-secret-token","nested":{"value":"prefix top-secret-token suffix"},"items":["safe","top-secret-token"],"number":7}`)
	}))
	defer server.Close()
	node := New(Options{LookupEnv: envLookup(map[string]string{webhookURLEnv: server.URL, webhookTokenEnv: token})})
	result, err := node.Execute(context.Background(), validRequest(map[string]any{"ok": true}))
	if err != nil {
		t.Fatal(err)
	}
	wantBody := map[string]any{
		"plain":  "[REDACTED]",
		"nested": map[string]any{"value": "prefix [REDACTED] suffix"},
		"items":  []any{"safe", "[REDACTED]"},
		"number": float64(7),
	}
	if !reflect.DeepEqual(result.Outputs["body"], wantBody) {
		t.Fatalf("body=%#v want=%#v", result.Outputs["body"], wantBody)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), "Authorization") {
		t.Fatalf("token or header leaked: %s", encoded)
	}
}

type captureRegistrar struct {
	node agentnode.Node
}

func (registrar *captureRegistrar) Register(node agentnode.Node) error {
	registrar.node = node
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func validRequest(body any) agentnode.Request {
	return agentnode.Request{
		Config: json.RawMessage(`{"path":"hook"}`),
		Inputs: map[string][]any{"body": {body}},
	}
}

func jsonStringOfEncodedSize(size int) string {
	return strings.Repeat("x", size-2)
}

func assertEmptyResult(t *testing.T, result agentnode.Result) {
	t.Helper()
	if len(result.Outputs) != 0 || len(result.ActivePorts) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func assertSafeErrorChain(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	for current := err; current != nil; current = errors.Unwrap(current) {
		for _, value := range forbidden {
			if value != "" && strings.Contains(current.Error(), value) {
				t.Fatalf("error chain leaked %q in %T: %v", value, current, current)
			}
		}
	}
}

func assertNodeError(t *testing.T, err error, kind agentnode.ErrorKind, code string) {
	t.Helper()
	var nodeErr *agentnode.NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Kind != kind || nodeErr.Code != code {
		t.Fatalf("error=%v kind=%q, want %s/%s", err, agentnode.KindOf(err), kind, code)
	}
}
