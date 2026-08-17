package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

const maxHTTPResponseBytes = 1 << 20

var (
	ErrSensitiveHeaderMustUseEnv = errors.New("sensitive HTTP header must use environment variable")
	ErrEnvironmentValueMissing   = errors.New("HTTP header environment value missing")
	ErrHTTPResponseTooLarge      = errors.New("HTTP response too large")
)

type HTTPOptions struct {
	AllowPrivateNetwork bool
	LookupIP            lookupIPFunc
	LookupEnv           func(string) (string, bool)
	Client              *http.Client
}

type httpNode struct {
	allowPrivateNetwork bool
	lookupIP            lookupIPFunc
	lookupEnv           func(string) (string, bool)
	client              *http.Client
}

type httpConfig struct {
	Method    string       `json:"method"`
	URL       string       `json:"url"`
	Headers   []httpHeader `json:"headers"`
	TimeoutMS int          `json:"timeoutMs,omitempty"`
}

type httpHeader struct {
	Name        string `json:"name"`
	ValueSource string `json:"valueSource"`
	Value       string `json:"value,omitempty"`
	EnvName     string `json:"envName,omitempty"`
}

func NewHTTP(options HTTPOptions) *httpNode {
	lookupIP := options.LookupIP
	if lookupIP == nil {
		lookupIP = net.DefaultResolver.LookupIP
	}
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	return &httpNode{
		allowPrivateNetwork: options.AllowPrivateNetwork,
		lookupIP:            lookupIP,
		lookupEnv:           lookupEnv,
		client:              options.Client,
	}
}

func (*httpNode) Definition() domain.NodeDefinition {
	return domain.NodeDefinition{
		Type:        "http",
		Version:     "1",
		Title:       "HTTP",
		Description: "调用受网络策略保护的 HTTP 接口",
		Category:    "集成",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{
            "method":{"type":"string","enum":["GET","POST","PUT","PATCH","DELETE"],"title":"方法"},
            "url":{"type":"string","minLength":1,"title":"URL"},
            "headers":{"type":"array","default":[],"items":{"type":"object","properties":{"name":{"type":"string"},"valueSource":{"type":"string","enum":["literal","env"]},"value":{"type":"string"},"envName":{"type":"string"}},"required":["name","valueSource"],"additionalProperties":false}},
            "timeoutMs":{"type":"integer","minimum":1,"maximum":30000,"default":10000,"title":"超时毫秒"}
          },
          "required":["method","url","headers"],
          "additionalProperties":false
        }`),
		Inputs: []domain.PortDefinition{{Key: "body", Title: "请求体", Type: domain.TypeAny, Cardinality: domain.CardinalityOne}},
		Outputs: []domain.PortDefinition{
			{Key: "status", Title: "状态码", Type: domain.TypeNumber, Cardinality: domain.CardinalityOne},
			{Key: "headers", Title: "响应头", Type: domain.TypeJSON, Cardinality: domain.CardinalityOne},
			{Key: "body", Title: "响应体", Type: domain.TypeAny, Cardinality: domain.CardinalityOne},
		},
	}
}

func (node *httpNode) Resolve(config json.RawMessage) (domain.ResolvedPorts, error) {
	if _, err := node.parseConfig(config); err != nil {
		return domain.ResolvedPorts{}, err
	}
	definition := node.Definition()
	return domain.ResolvedPorts{Inputs: definition.Inputs, Outputs: definition.Outputs}, nil
}

func (node *httpNode) Execute(ctx context.Context, request domain.NodeRequest) (domain.NodeResult, error) {
	config, err := node.parseConfig(request.Config)
	if err != nil {
		return domain.NodeResult{}, err
	}
	if _, err := node.validateURL(ctx, config.URL); err != nil {
		return domain.NodeResult{}, err
	}

	var body io.Reader
	if values := request.Inputs["body"]; len(values) > 0 {
		if len(values) != 1 {
			return domain.NodeResult{}, fmt.Errorf("%w: body", ErrInputCardinality)
		}
		encoded, err := json.Marshal(values[0])
		if err != nil {
			return domain.NodeResult{}, fmt.Errorf("encode HTTP body: %w", err)
		}
		body = bytes.NewReader(encoded)
	}

	timeout := time.Duration(config.TimeoutMS) * time.Millisecond
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, config.Method, config.URL, body)
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("create HTTP request: %w", err)
	}
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	for _, header := range config.Headers {
		value := header.Value
		if header.ValueSource == "env" {
			var exists bool
			value, exists = node.lookupEnv(header.EnvName)
			if !exists {
				return domain.NodeResult{}, fmt.Errorf("%w: %s", ErrEnvironmentValueMissing, header.EnvName)
			}
		}
		httpRequest.Header.Set(header.Name, value)
	}

	response, err := node.newClient().Do(httpRequest)
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("send HTTP request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
	if err != nil {
		return domain.NodeResult{}, fmt.Errorf("read HTTP response: %w", err)
	}
	if len(responseBody) > maxHTTPResponseBytes {
		return domain.NodeResult{}, ErrHTTPResponseTooLarge
	}

	var decodedBody any = string(responseBody)
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		if err := json.Unmarshal(responseBody, &decodedBody); err != nil {
			return domain.NodeResult{}, fmt.Errorf("decode HTTP JSON response: %w", err)
		}
	}
	return domain.NodeResult{Outputs: map[string]any{
		"status":  float64(response.StatusCode),
		"headers": map[string][]string(response.Header),
		"body":    decodedBody,
	}}, nil
}

func (node *httpNode) parseConfig(raw json.RawMessage) (httpConfig, error) {
	var config httpConfig
	if err := decodeConfig(raw, &config); err != nil {
		return httpConfig{}, err
	}
	config.Method = strings.ToUpper(config.Method)
	switch config.Method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return httpConfig{}, fmt.Errorf("%w: unsupported HTTP method", ErrInvalidConfig)
	}
	if config.TimeoutMS == 0 {
		config.TimeoutMS = 10000
	}
	if config.TimeoutMS < 1 || config.TimeoutMS > 30000 {
		return httpConfig{}, fmt.Errorf("%w: timeoutMs must be between 1 and 30000", ErrInvalidConfig)
	}
	for _, header := range config.Headers {
		if strings.TrimSpace(header.Name) == "" {
			return httpConfig{}, fmt.Errorf("%w: header name is required", ErrInvalidConfig)
		}
		switch header.ValueSource {
		case "literal":
			if sensitiveHeader(header.Name) {
				return httpConfig{}, fmt.Errorf("%w: %s", ErrSensitiveHeaderMustUseEnv, header.Name)
			}
			if header.EnvName != "" {
				return httpConfig{}, fmt.Errorf("%w: literal header cannot contain envName", ErrInvalidConfig)
			}
		case "env":
			if header.EnvName == "" || header.Value != "" {
				return httpConfig{}, fmt.Errorf("%w: env header must contain only envName", ErrInvalidConfig)
			}
		default:
			return httpConfig{}, fmt.Errorf("%w: invalid header valueSource", ErrInvalidConfig)
		}
	}
	return config, nil
}
