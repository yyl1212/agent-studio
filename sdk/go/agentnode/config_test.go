package agentnode_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type testConfig struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func TestDecodeConfigStrictlyDecodesOneObject(t *testing.T) {
	var got testConfig
	err := agentnode.DecodeConfig(json.RawMessage(`{"name":"demo","enabled":true}`), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got != (testConfig{Name: "demo", Enabled: true}) {
		t.Fatalf("config = %#v", got)
	}
}

func TestDecodeConfigTreatsEmptyInputAsObject(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, []byte(" \n\t")} {
		var got testConfig
		if err := agentnode.DecodeConfig(raw, &got); err != nil {
			t.Fatalf("DecodeConfig(%q) error = %v", raw, err)
		}
		if got != (testConfig{}) {
			t.Fatalf("DecodeConfig(%q) = %#v", raw, got)
		}
	}
}

func TestDecodeConfigRejectsUnknownTrailingAndWrongType(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
	}{
		{name: "unknown field", raw: json.RawMessage(`{"name":"demo","extra":true}`)},
		{name: "trailing value", raw: json.RawMessage(`{"name":"demo"}{}`)},
		{name: "wrong type", raw: json.RawMessage(`{"enabled":"yes"}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got testConfig
			err := agentnode.DecodeConfig(test.raw, &got)
			if err == nil {
				t.Fatal("DecodeConfig() succeeded, want error")
			}
			var nodeErr *agentnode.NodeError
			if !errors.As(err, &nodeErr) {
				t.Fatalf("error type = %T, want *NodeError", err)
			}
			if nodeErr.Kind != agentnode.ErrorKindConfig || nodeErr.Code != "invalid_config" {
				t.Fatalf("node error = %#v", nodeErr)
			}
		})
	}
}

func TestMustSchemaReturnsIndependentJSONBytes(t *testing.T) {
	first := agentnode.MustSchema(`{"type":"object"}`)
	second := agentnode.MustSchema(`{"type":"object"}`)
	first[0] = '['
	if string(second) != `{"type":"object"}` {
		t.Fatalf("second schema changed to %q", second)
	}
}

func TestMustSchemaPanicsForInvalidJSON(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustSchema() did not panic")
		}
	}()
	agentnode.MustSchema(`{"type":`)
}
