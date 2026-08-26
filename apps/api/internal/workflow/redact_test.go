package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSecretRedactorRemovesSecretMovedToOrdinaryValuesAndObjectKeys(t *testing.T) {
	report := RedactWithReport(map[string]any{"webhookToken": "do-not-persist"})
	redactor := NewSecretRedactor(report.SecretValues)
	safe := redactor.RedactWithReport(map[string]any{
		"result": "do-not-persist",
		"nested": []any{map[string]any{
			"do-not-persist": "value",
			"visible":        "ordinary",
		}},
	})
	want := map[string]any{
		"result": redactedValue,
		"nested": []any{map[string]any{
			redactedValue: "value",
			"visible":     "ordinary",
		}},
	}
	if !reflect.DeepEqual(safe.Value, want) {
		t.Fatalf("safe value=%#v, want %#v", safe.Value, want)
	}
	if !reflect.DeepEqual(safe.Paths, []string{"/nested/0/[REDACTED]", "/result"}) {
		t.Fatalf("safe paths=%v", safe.Paths)
	}
	if strings.Contains(fmt.Sprint(safe.Value), "do-not-persist") || strings.Contains(strings.Join(safe.Paths, ","), "do-not-persist") {
		t.Fatal("secret leaked through value or JSON Pointer path")
	}
}

func TestSecretRedactorMatchesCompositeAndScalarSecretsWithoutGlobalEmptyRedaction(t *testing.T) {
	report := RedactWithReport(map[string]any{
		"apiToken":    map[string]any{"value": "nested-secret"},
		"password":    []any{json.Number("9007199254740993"), true},
		"secretEmpty": "",
	})
	redactor := NewSecretRedactor(report.SecretValues)
	safe := redactor.RedactWithReport(map[string]any{
		"objectEcho": map[string]any{"value": "nested-secret"},
		"numberEcho": json.Number("9007199254740993"),
		"boolEcho":   true,
		"empty":      "",
		"ordinary":   "visible",
	})
	want := map[string]any{
		"objectEcho": redactedValue,
		"numberEcho": redactedValue,
		"boolEcho":   redactedValue,
		"empty":      "",
		"ordinary":   "visible",
	}
	if !reflect.DeepEqual(safe.Value, want) {
		t.Fatalf("safe value=%#v, want %#v", safe.Value, want)
	}
}

func TestSecretRedactorUsesDeterministicCollisionKeysAndCopiesContainers(t *testing.T) {
	report := RedactWithReport(map[string]any{"token": "A-secret-key"})
	input := map[string]any{
		"nested": map[string]any{
			redactedValue:  "existing",
			"A-secret-key": "hidden-key-value",
		},
	}
	safe := NewSecretRedactor(report.SecretValues).RedactWithReport(input)
	want := map[string]any{"nested": map[string]any{
		redactedValue:        "existing",
		redactedValue + "#2": "hidden-key-value",
	}}
	if !reflect.DeepEqual(safe.Value, want) {
		t.Fatalf("safe value=%#v, want %#v", safe.Value, want)
	}
	safe.Value.(map[string]any)["nested"].(map[string]any)[redactedValue] = "changed"
	if input["nested"].(map[string]any)[redactedValue] != "existing" {
		t.Fatal("secret redactor mutated the source container")
	}
}

func TestSecretRedactorIncludesValuesProtectedByDepthLimit(t *testing.T) {
	var input any = "deep-secret"
	for depth := 0; depth < maxRedactDepth; depth++ {
		input = map[string]any{"safe": input}
	}
	report := RedactWithReport(input)
	if strings.Contains(fmt.Sprint(report.Value), "deep-secret") || len(report.SecretValues) != 1 {
		t.Fatalf("depth-limited report=%+v", report)
	}
	safe := NewSecretRedactor(report.SecretValues).RedactWithReport(map[string]any{"echo": "deep-secret"})
	if got := safe.Value.(map[string]any)["echo"]; got != redactedValue {
		t.Fatalf("depth-limited secret echo=%v", got)
	}
}

func TestRedactWithReportReturnsEscapedJSONPointers(t *testing.T) {
	report := RedactWithReport(map[string]any{
		"safe": map[string]any{"api/token": "secret", "tilde~password": "secret"},
	})
	want := []string{"/safe/api~1token", "/safe/tilde~0password"}
	if !reflect.DeepEqual(report.Paths, want) {
		t.Fatalf("paths=%v", report.Paths)
	}
	if strings.Contains(fmt.Sprint(report.Value), "secret") {
		t.Fatal("secret leaked")
	}
}

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

func TestRedactCoversNamedMapsAndSlices(t *testing.T) {
	type namedStringMap map[string]string
	type namedSlice []namedStringMap
	type namedDetails map[string]any

	input := namedDetails{
		"nested": namedSlice{{"api_token": "top-secret", "safe": "visible"}},
	}
	encoded := reflect.ValueOf(Redact(input))
	if encoded.Kind() != reflect.Map {
		t.Fatalf("redacted kind=%s", encoded.Kind())
	}
	got := Redact(input)
	want := map[string]any{
		"nested": []any{map[string]any{"api_token": "[REDACTED]", "safe": "visible"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted=%#v, want %#v", got, want)
	}
}
