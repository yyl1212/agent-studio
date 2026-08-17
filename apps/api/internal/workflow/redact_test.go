package workflow

import (
	"net/http"
	"reflect"
	"testing"
)

func TestRedactRecursivelyCopiesSensitiveFields(t *testing.T) {
	input := map[string]any{
		"Authorization": "Bearer secret",
		"nested": []any{map[string]any{
			"api-key": "key",
			"safe":    "visible",
		}},
	}
	got := Redact(input)
	want := map[string]any{
		"Authorization": "[REDACTED]",
		"nested": []any{map[string]any{
			"api-key": "[REDACTED]",
			"safe":    "visible",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted=%#v", got)
	}
	if input["Authorization"] != "Bearer secret" {
		t.Fatal("Redact mutated source")
	}
}

func TestRedactCoversHTTPHeaderValues(t *testing.T) {
	input := http.Header{
		"Set-Cookie":   []string{"session=secret"},
		"Content-Type": []string{"application/json"},
	}
	got := Redact(input)
	want := http.Header{
		"Set-Cookie":   []string{"[REDACTED]"},
		"Content-Type": []string{"application/json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted headers=%#v", got)
	}
}

func TestRedactMatchesSensitiveKeyFragments(t *testing.T) {
	input := map[string]any{
		"databasePassword": "top-secret",
		"nested": []any{
			map[string]any{"api_key": "key", "safe": "visible"},
		},
	}
	want := map[string]any{
		"databasePassword": "[REDACTED]",
		"nested": []any{
			map[string]any{"api_key": "[REDACTED]", "safe": "visible"},
		},
	}
	if got := Redact(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted = %#v, want %#v", got, want)
	}
}
