package webhook

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
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

func assertNodeError(t *testing.T, err error, kind agentnode.ErrorKind, code string) {
	t.Helper()
	var nodeErr *agentnode.NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Kind != kind || nodeErr.Code != code {
		t.Fatalf("error=%v kind=%q, want %s/%s", err, agentnode.KindOf(err), kind, code)
	}
}
