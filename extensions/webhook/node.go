package webhook

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	defaultTimeoutMS  = 5000
	maxBodyBytes      = 1 << 20
	maxUnescapePasses = 8
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

type Config struct {
	Path      string
	TimeoutMS int
}

type configJSON struct {
	Path      string `json:"path"`
	TimeoutMS *int   `json:"timeoutMs,omitempty"`
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
