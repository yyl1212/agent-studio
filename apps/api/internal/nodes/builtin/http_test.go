package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHTTPRejectsPrivateAddressByDefault(t *testing.T) {
	node := NewHTTP(HTTPOptions{AllowPrivateNetwork: false, LookupIP: net.DefaultResolver.LookupIP})
	config := json.RawMessage(`{"method":"GET","url":"http://127.0.0.1:8080","headers":[]}`)
	_, err := node.Execute(context.Background(), domain.NodeRequest{Config: config})
	if !errors.Is(err, ErrPrivateAddressBlocked) {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPRejectsHostnameResolvingToPrivateAddress(t *testing.T) {
	lookup := func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.8")}, nil
	}
	node := NewHTTP(HTTPOptions{LookupIP: lookup})
	config := json.RawMessage(`{"method":"GET","url":"http://private.internal/data","headers":[]}`)
	_, err := node.Execute(context.Background(), domain.NodeRequest{Config: config})
	if !errors.Is(err, ErrPrivateAddressBlocked) {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPRequiresEnvForSensitiveHeader(t *testing.T) {
	node := NewHTTP(HTTPOptions{})
	config := json.RawMessage(`{"method":"GET","url":"https://example.com","headers":[{"name":"Authorization","valueSource":"literal","value":"secret"}]}`)
	if _, err := node.Resolve(config); !errors.Is(err, ErrSensitiveHeaderMustUseEnv) {
		t.Fatalf("error=%v", err)
	}
}

func TestHTTPRechecksRedirectAndRequiresExistingEnvironmentValue(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://127.0.0.1/admin"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}
	lookup := func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	node := NewHTTP(HTTPOptions{Client: client, LookupIP: lookup})
	config := json.RawMessage(`{"method":"GET","url":"https://public.example/start","headers":[]}`)
	if _, err := node.Execute(context.Background(), domain.NodeRequest{Config: config}); !errors.Is(err, ErrPrivateAddressBlocked) {
		t.Fatalf("redirect error=%v", err)
	}

	node = NewHTTP(HTTPOptions{AllowPrivateNetwork: true, Client: client, LookupEnv: func(string) (string, bool) { return "", false }})
	config = json.RawMessage(`{"method":"GET","url":"https://public.example","headers":[{"name":"Authorization","valueSource":"env","envName":"AGENT_TOKEN"}]}`)
	if _, err := node.Execute(context.Background(), domain.NodeRequest{Config: config}); !errors.Is(err, ErrEnvironmentValueMissing) {
		t.Fatalf("environment error=%v", err)
	}
}

func TestHTTPRejectsCrossOriginRedirectBeforeForwardingSecret(t *testing.T) {
	secretReceived := false
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		secretReceived = request.Header.Get("X-API-Key") != ""
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer source.Close()

	node := NewHTTP(HTTPOptions{
		AllowPrivateNetwork: true,
		LookupEnv: func(name string) (string, bool) {
			return "top-secret", name == "UPSTREAM_API_KEY"
		},
	})
	config := json.RawMessage(fmt.Sprintf(`{"method":"GET","url":%q,"headers":[{"name":"X-API-Key","valueSource":"env","envName":"UPSTREAM_API_KEY"}]}`, source.URL))
	_, err := node.Execute(context.Background(), domain.NodeRequest{Config: config})
	if !errors.Is(err, ErrCrossOriginRedirect) {
		t.Fatalf("error=%v, want ErrCrossOriginRedirect", err)
	}
	if secretReceived {
		t.Fatal("sensitive header reached redirect target")
	}
}

func TestHTTPAllowsSameOriginRedirect(t *testing.T) {
	secretReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/start" {
			http.Redirect(writer, request, "/final", http.StatusFound)
			return
		}
		secretReceived = request.Header.Get("Authorization") == "Bearer top-secret"
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	node := NewHTTP(HTTPOptions{
		AllowPrivateNetwork: true,
		LookupEnv: func(name string) (string, bool) {
			return "Bearer top-secret", name == "UPSTREAM_AUTH"
		},
	})
	config := json.RawMessage(fmt.Sprintf(`{"method":"GET","url":%q,"headers":[{"name":"Authorization","valueSource":"env","envName":"UPSTREAM_AUTH"}]}`, server.URL+"/start"))
	result, err := node.Execute(context.Background(), domain.NodeRequest{Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if !secretReceived || result.Outputs["status"] != float64(http.StatusOK) {
		t.Fatalf("secretReceived=%v outputs=%v", secretReceived, result.Outputs)
	}
}

func TestHTTPLimitsResponseAndDecodesJSON(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantErr     error
	}{
		{name: "oversized", body: strings.Repeat("x", 1<<20+1), contentType: "text/plain", wantErr: ErrHTTPResponseTooLarge},
		{name: "json", body: `{"ok":true}`, contentType: "application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{test.contentType}},
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			})}
			node := NewHTTP(HTTPOptions{AllowPrivateNetwork: true, Client: client})
			result, err := node.Execute(context.Background(), domain.NodeRequest{Config: json.RawMessage(`{"method":"GET","url":"https://example.com","headers":[]}`)})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && result.Outputs["body"].(map[string]any)["ok"] != true {
				t.Fatalf("body=%v", result.Outputs["body"])
			}
		})
	}
}
