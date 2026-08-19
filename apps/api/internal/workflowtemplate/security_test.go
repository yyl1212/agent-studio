package workflowtemplate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func TestSecurityIssuesRejectKnownSecretChannels(t *testing.T) {
	definition := agentnode.Definition{
		Type: "extension.secure", Version: "1.0.0",
		ConfigSchema: json.RawMessage(`{
          "$defs":{"credential":{"type":"string","writeOnly":true}},
          "type":"object","properties":{"value":{"$ref":"#/$defs/credential"}}
        }`),
	}
	tests := []struct {
		name        string
		node        domain.Node
		definitions []agentnode.Definition
		wantCode    string
	}{
		{name: "max tokens is ordinary", node: domain.Node{ID: "llm", Type: "llm", TypeVersion: "1", Config: json.RawMessage(`{"maxTokens":1024}`)}},
		{name: "credential key", node: domain.Node{ID: "n", Type: "x", TypeVersion: "1", Config: json.RawMessage(`{"api_token":"top-secret"}`)}, wantCode: "TEMPLATE_SECRET_CONFIG_FOUND"},
		{name: "write only ref", node: domain.Node{ID: "n", Type: "extension.secure", TypeVersion: "1.0.0", Config: json.RawMessage(`{"value":"top-secret"}`)}, definitions: []agentnode.Definition{definition}, wantCode: "TEMPLATE_SECRET_CONFIG_FOUND"},
		{name: "http literal auth", node: domain.Node{ID: "http", Type: "http", TypeVersion: "1", Config: json.RawMessage(`{"method":"GET","url":"https://example.com","headers":[{"name":"Authorization","valueSource":"literal","value":"top-secret"}]}`)}, wantCode: "TEMPLATE_SECRET_CONFIG_FOUND"},
		{name: "http env auth", node: domain.Node{ID: "http", Type: "http", TypeVersion: "1", Config: json.RawMessage(`{"method":"GET","url":"https://example.com","headers":[{"name":"Authorization","valueSource":"env","envName":"UPSTREAM_AUTH"}]}`)}},
		{name: "http token query", node: domain.Node{ID: "http", Type: "http", TypeVersion: "1", Config: json.RawMessage(`{"method":"GET","url":"https://example.com?access_token=top-secret","headers":[]}`)}, wantCode: "TEMPLATE_SECRET_CONFIG_FOUND"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := securityIssues(domain.Graph{SchemaVersion: 1, Nodes: []domain.Node{test.node}}, definitionIndex(test.definitions))
			if test.wantCode == "" && len(issues) != 0 {
				t.Fatalf("issues=%+v", issues)
			}
			if test.wantCode != "" && (len(issues) == 0 || issues[0].Code != test.wantCode) {
				t.Fatalf("issues=%+v", issues)
			}
			encoded, err := json.Marshal(issues)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("top-secret")) || bytes.Contains(encoded, []byte("example.com")) {
				t.Fatalf("secret leaked: %s", encoded)
			}
		})
	}
}

func TestSecurityIssuesUsesPreciseCredentialKeyMatching(t *testing.T) {
	for _, key := range []string{"maxTokens", "tokenizer", "authorship"} {
		t.Run(key, func(t *testing.T) {
			config := json.RawMessage(fmt.Sprintf(`{"%s":"ordinary"}`, key))
			issues := securityIssues(domain.Graph{Nodes: []domain.Node{{ID: "n", Config: config}}}, nil)
			if len(issues) != 0 {
				t.Fatalf("ordinary key rejected: %+v", issues)
			}
		})
	}
}

func TestSecurityIssuesTraversesSchemaCompositionAndStopsReferenceCycles(t *testing.T) {
	definition := agentnode.Definition{
		Type: "secure", Version: "1",
		ConfigSchema: json.RawMessage(`{
          "$defs": {
            "secret": {"writeOnly": true},
            "cycle": {"allOf": [{"$ref": "#/$defs/cycle"}]}
          },
          "type": "object",
          "properties": {
            "nested": {
              "anyOf": [{"type": "array", "items": {"oneOf": [{"$ref": "#/$defs/secret"}]}}]
            },
            "cycle": {"$ref": "#/$defs/cycle"},
            "external": {"$ref": "https://example.invalid/schema.json"}
          }
        }`),
	}
	node := domain.Node{ID: "n", Type: "secure", TypeVersion: "1", Config: json.RawMessage(`{"nested":["hidden"],"cycle":{},"external":"ordinary"}`)}
	issues := securityIssues(domain.Graph{Nodes: []domain.Node{node}}, definitionIndex([]agentnode.Definition{definition}))
	if len(issues) != 1 || issues[0].Path != "config.nested[0]" {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestSecurityIssuesRejectsAgentStudioSecretSchemaExtension(t *testing.T) {
	definition := agentnode.Definition{
		Type: "secure-extension", Version: "1",
		ConfigSchema: json.RawMessage(`{
          "type":"object",
          "properties":{"credential":{"type":"string","x-agent-studio-secret":true}}
        }`),
	}
	node := domain.Node{
		ID: "n", Type: "secure-extension", TypeVersion: "1",
		Config: json.RawMessage(`{"credential":"hidden-value"}`),
	}
	issues := securityIssues(domain.Graph{Nodes: []domain.Node{node}}, definitionIndex([]agentnode.Definition{definition}))
	if len(issues) != 1 || issues[0].Code != "TEMPLATE_SECRET_CONFIG_FOUND" || issues[0].Path != "config.credential" {
		t.Fatalf("issues=%+v", issues)
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("hidden-value")) {
		t.Fatalf("secret leaked: %s", encoded)
	}
}

func TestConfigDepthRejectsMoreThanMaximum(t *testing.T) {
	raw := json.RawMessage(strings.Repeat(`{"child":`, MaxDepth) + `null` + strings.Repeat(`}`, MaxDepth))
	depth, err := configDepth(raw)
	if err != nil {
		t.Fatal(err)
	}
	if depth <= MaxDepth {
		t.Fatalf("depth=%d, want more than %d", depth, MaxDepth)
	}
	issues := securityIssues(domain.Graph{Nodes: []domain.Node{{ID: "deep", Config: raw}}}, nil)
	if len(issues) != 1 || issues[0].Code != "TEMPLATE_LIMIT_EXCEEDED" {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestConfigDepthAndSecurityAcceptLargeJSONExponent(t *testing.T) {
	raw := json.RawMessage(`{"value":1e400}`)
	depth, err := configDepth(raw)
	if err != nil {
		t.Fatalf("valid JSON number rejected: %v", err)
	}
	if depth != 2 {
		t.Fatalf("depth=%d, want 2", depth)
	}
	issues := securityIssues(domain.Graph{Nodes: []domain.Node{{ID: "number", Config: raw}}}, nil)
	if len(issues) != 0 {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestSecurityIssuesRejectsInvalidConfigWithoutEchoingInput(t *testing.T) {
	issues := securityIssues(domain.Graph{Nodes: []domain.Node{{ID: "broken", Config: json.RawMessage(`{"password":"top-secret"`)}}}, nil)
	if len(issues) != 1 || issues[0].Code != "TEMPLATE_CONFIG_JSON_INVALID" {
		t.Fatalf("issues=%+v", issues)
	}
	encoded, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("top-secret")) {
		t.Fatalf("secret leaked: %s", encoded)
	}
}
