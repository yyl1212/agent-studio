package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	defaultTimeoutMS  = 5000
	maxBodyBytes      = 1 << 20
	maxUnescapePasses = 8
	webhookURLEnv     = "AGENT_STUDIO_WEBHOOK_URL"
	webhookTokenEnv   = "AGENT_STUDIO_WEBHOOK_TOKEN"
)

var (
	ErrInvalidConfig        = errors.New("invalid webhook configuration")
	ErrInvalidBody          = errors.New("invalid webhook body")
	ErrWebhookRejected      = errors.New("webhook request rejected")
	ErrUpstreamFailed       = errors.New("webhook upstream failed")
	ErrMissingConfiguration = errors.New("webhook configuration missing")
	ErrRedirectBlocked      = errors.New("webhook redirect blocked")
	ErrResponseTooLarge     = errors.New("webhook response too large")
	ErrInvalidResponse      = errors.New("invalid webhook response")
)

type Options struct {
	LookupEnv func(string) (string, bool)
	Client    *http.Client
}

type Node struct {
	lookupEnv func(string) (string, bool)
	client    *http.Client
}

type Config struct {
	Path      string
	TimeoutMS int
}

type configJSON struct {
	Path      string `json:"path"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
}

func New(options Options) *Node {
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	return &Node{lookupEnv: lookupEnv, client: options.Client}
}

func Register(registrar agentnode.Registrar) error {
	return registrar.Register(New(Options{}))
}

func (*Node) Definition() agentnode.Definition {
	return agentnode.Definition{
		Type:        "extension.webhook",
		Version:     "1.0.0",
		Title:       "Webhook",
		Description: "向运维配置的基地址发送受约束的 JSON POST 请求",
		Category:    "扩展",
		ConfigSchema: agentnode.MustSchema(`{
          "$schema":"https://json-schema.org/draft/2020-12/schema",
          "type":"object",
          "properties":{
            "path":{"type":"string","title":"相对路径","minLength":1},
            "timeoutMs":{"type":"integer","title":"超时毫秒","minimum":1,"maximum":30000,"default":5000}
          },
          "required":["path"],
          "additionalProperties":false,
          "x-ui-order":["path","timeoutMs"]
        }`),
		Inputs: []agentnode.Port{{
			Key: "body", Title: "请求体", Type: agentnode.DataTypeJSON,
			Required: true, Cardinality: agentnode.CardinalityOne,
		}},
		Outputs: []agentnode.Port{
			{Key: "status", Title: "状态码", Type: agentnode.DataTypeNumber, Cardinality: agentnode.CardinalityOne},
			{Key: "body", Title: "响应体", Type: agentnode.DataTypeJSON, Cardinality: agentnode.CardinalityOne},
		},
		Capabilities: []agentnode.Capability{agentnode.CapabilityNetwork, agentnode.CapabilitySecrets},
	}
}

func (node *Node) Resolve(config json.RawMessage) (agentnode.ResolvedPorts, error) {
	if _, err := parseConfig(config); err != nil {
		return agentnode.ResolvedPorts{}, err
	}
	definition := node.Definition()
	return agentnode.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *Node) Execute(ctx context.Context, request agentnode.Request) (agentnode.Result, error) {
	if err := ctx.Err(); err != nil {
		return agentnode.Result{}, mapContextError(err)
	}
	config, err := parseConfig(request.Config)
	if err != nil {
		return agentnode.Result{}, err
	}
	values := request.Inputs["body"]
	if len(values) != 1 {
		return agentnode.Result{}, invalidBodyError()
	}
	requestBody, err := json.Marshal(values[0])
	if err != nil || len(requestBody) > maxBodyBytes {
		return agentnode.Result{}, invalidBodyError()
	}

	rawBaseURL, exists := node.lookupEnv(webhookURLEnv)
	if !exists {
		return agentnode.Result{}, missingConfigurationError()
	}
	baseURL, err := parseBaseURL(rawBaseURL)
	if err != nil {
		return agentnode.Result{}, missingConfigurationError()
	}
	target := joinTarget(baseURL, config.Path)
	token, _ := node.lookupEnv(webhookTokenEnv)

	requestContext, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutMS)*time.Millisecond)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, target.String(), bytes.NewReader(requestBody))
	if err != nil {
		return agentnode.Result{}, missingConfigurationError()
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := node.requestClient().Do(httpRequest)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return agentnode.Result{}, canceledError()
		}
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return agentnode.Result{}, upstreamError(context.DeadlineExceeded)
		}
		if errors.Is(err, ErrRedirectBlocked) {
			return agentnode.Result{}, upstreamError(ErrRedirectBlocked)
		}
		return agentnode.Result{}, upstreamError()
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
	case response.StatusCode >= http.StatusBadRequest && response.StatusCode < http.StatusInternalServerError && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests:
		return agentnode.Result{}, rejectedError()
	default:
		return agentnode.Result{}, upstreamError()
	}
	if response.StatusCode == http.StatusNoContent {
		return successResult(response.StatusCode, nil), nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return agentnode.Result{}, canceledError()
		}
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return agentnode.Result{}, upstreamError(context.DeadlineExceeded)
		}
		return agentnode.Result{}, upstreamError()
	}
	if len(responseBody) > maxBodyBytes {
		return agentnode.Result{}, upstreamError(ErrResponseTooLarge)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return successResult(response.StatusCode, nil), nil
	}
	var decoded any
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return agentnode.Result{}, upstreamError(ErrInvalidResponse)
	}
	return successResult(response.StatusCode, redactToken(decoded, token)), nil
}

func parseConfig(raw json.RawMessage) (Config, error) {
	var decoded configJSON
	if err := agentnode.DecodeConfig(raw, &decoded); err != nil {
		return Config{}, invalidConfigError()
	}
	path, err := validateRelativePath(decoded.Path)
	if err != nil {
		return Config{}, invalidConfigError()
	}
	timeoutMS := defaultTimeoutMS
	if decoded.TimeoutMS != nil {
		timeoutMS = *decoded.TimeoutMS
	}
	if timeoutMS < 1 || timeoutMS > 30000 {
		return Config{}, invalidConfigError()
	}
	return Config{Path: path, TimeoutMS: timeoutMS}, nil
}

func validateRelativePath(raw string) (string, error) {
	current := strings.TrimSpace(raw)
	if current == "" {
		return "", ErrInvalidConfig
	}
	for range maxUnescapePasses {
		if err := validatePathValue(current); err != nil {
			return "", ErrInvalidConfig
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return "", ErrInvalidConfig
		}
		if decoded == current {
			return current, nil
		}
		current = decoded
	}
	if err := validatePathValue(current); err != nil {
		return "", ErrInvalidConfig
	}
	decoded, err := url.PathUnescape(current)
	if err != nil || decoded != current {
		return "", ErrInvalidConfig
	}
	return current, nil
}

func validatePathValue(value string) error {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || strings.ContainsAny(value, "?#") {
		return ErrInvalidConfig
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ErrInvalidConfig
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return ErrInvalidConfig
		}
	}
	return nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, "?#") {
		return nil, ErrMissingConfiguration
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, ErrMissingConfiguration
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, ErrMissingConfiguration
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, nil
}

func joinTarget(base *url.URL, relativePath string) *url.URL {
	return base.JoinPath(relativePath)
}

func invalidConfigError() error {
	return agentnode.NewError(agentnode.ErrorKindConfig, "invalid_config", ErrInvalidConfig, nil)
}

func (node *Node) requestClient() *http.Client {
	client := http.Client{}
	if node.client != nil {
		client = *node.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return ErrRedirectBlocked
	}
	return &client
}

func successResult(status int, body any) agentnode.Result {
	return agentnode.Result{Outputs: map[string]any{"status": float64(status), "body": body}}
}

func redactToken(value any, token string) any {
	switch typed := value.(type) {
	case string:
		return redactString(typed, token)
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactToken(item, token)
		}
		return redacted
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			redactedKey := redactString(key, token)
			uniqueKey := redactedKey
			for suffix := 2; ; suffix++ {
				if _, exists := redacted[uniqueKey]; !exists {
					break
				}
				uniqueKey = redactedKey + "#" + strconv.Itoa(suffix)
			}
			redacted[uniqueKey] = redactToken(typed[key], token)
		}
		return redacted
	default:
		return value
	}
}

func redactString(value, token string) string {
	if token == "" {
		return value
	}
	return strings.ReplaceAll(value, token, "[REDACTED]")
}

func nodeError(kind agentnode.ErrorKind, code string, cause error) error {
	return agentnode.NewError(kind, code, cause, nil)
}

func invalidBodyError() error {
	return nodeError(agentnode.ErrorKindInput, "invalid_body", ErrInvalidBody)
}

func missingConfigurationError() error {
	return nodeError(agentnode.ErrorKindInternal, "missing_webhook_configuration", ErrMissingConfiguration)
}

func rejectedError() error {
	return nodeError(agentnode.ErrorKindInput, "webhook_rejected", ErrWebhookRejected)
}

func upstreamError(causes ...error) error {
	safe := []error{ErrUpstreamFailed}
	safe = append(safe, causes...)
	return nodeError(agentnode.ErrorKindTemporary, "upstream_failed", errors.Join(safe...))
}

func canceledError() error {
	return nodeError(agentnode.ErrorKindCanceled, "run_canceled", context.Canceled)
}

func mapContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return canceledError()
	}
	return upstreamError(context.DeadlineExceeded)
}

var _ agentnode.Node = (*Node)(nil)
