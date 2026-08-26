package nodesecurity

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInspectConfigFindsSchemaHeuristicAndHTTPSensitivePaths(t *testing.T) {
	schema := json.RawMessage(`{
      "$defs":{"credential":{"type":"string","writeOnly":true}},
      "type":"object",
      "properties":{
        "credential":{"$ref":"#/$defs/credential"},
        "nested":{"type":"object","properties":{"token":{"type":"string","x-agent-studio-secret":true}}}
      }
    }`)
	matches, err := InspectConfig("extension", "1",
		json.RawMessage(`{"credential":"","nested":{"token":"secret"},"api_key":"literal"}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, matches, []Match{
		{Pointer: "/api_key", LegacyPath: "config.api_key", Source: SourceSensitiveKey, HasValue: true},
		{Pointer: "/credential", LegacyPath: "config.credential", Source: SourceSchema, HasValue: false},
		{Pointer: "/nested/token", LegacyPath: "config.nested.token", Source: SourceSchema, HasValue: true},
		{Pointer: "/nested/token", LegacyPath: "config.nested.token", Source: SourceSensitiveKey, HasValue: true},
	})
}

func TestInspectConfigFindsHTTPURLAndHeader(t *testing.T) {
	matches, err := InspectConfig("http", "1", json.RawMessage(`{
      "url":"https://example.test/path?access_token=value",
      "headers":[{"name":"Authorization","valueSource":"literal","value":"Bearer value"}]
    }`), json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, matches, []Match{
		{Pointer: "/headers/0/value", LegacyPath: "config.headers[0].value", Source: SourceHTTPHeader, HasValue: true},
		{Pointer: "/url", LegacyPath: "config.url", Source: SourceHTTPURL, HasValue: true},
	})
}

func TestInspectConfigTraversesCompositionEscapesPointersAndDeduplicates(t *testing.T) {
	schema := json.RawMessage(`{
      "$defs":{
        "secret":{"writeOnly":true},
        "cycle":{"allOf":[{"$ref":"#/$defs/cycle"}]}
      },
      "type":"object",
      "properties":{
        "a/b":{"type":"array","items":{"oneOf":[{"$ref":"#/$defs/secret"},{"$ref":"#/$defs/secret"}]}},
        "cycle":{"$ref":"#/$defs/cycle"}
      }
    }`)
	matches, err := InspectConfig("extension", "1", json.RawMessage(`{"a/b":["value"],"cycle":{}}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	assertMatches(t, matches, []Match{{
		Pointer: "/a~1b/0", LegacyPath: "config.a/b[0]", Source: SourceSchema, HasValue: true,
	}})
}

func TestInspectConfigRejectsInvalidOrTrailingJSON(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"token":`),
		json.RawMessage(`{"token":"value"} {}`),
	} {
		if _, err := InspectConfig("extension", "1", raw, json.RawMessage(`{"type":"object"}`)); err == nil {
			t.Fatalf("invalid config accepted: %q", raw)
		}
	}
	if _, err := InspectConfig("extension", "1", json.RawMessage(`{}`), json.RawMessage(`{"type":"object"} {}`)); err == nil {
		t.Fatal("trailing schema JSON accepted")
	}
}

func assertMatches(t *testing.T, got, want []Match) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches=%+v, want %+v", got, want)
	}
}
