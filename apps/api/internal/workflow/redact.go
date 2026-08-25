package workflow

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const redactedValue = "[REDACTED]"
const maxRedactDepth = 64

func Redact(value any) any {
	return RedactWithReport(value).Value
}

type RedactionReport struct {
	Value        any
	Paths        []string
	SecretValues []any `json:"-"`
}

func RedactWithReport(value any) RedactionReport {
	paths := make([]string, 0)
	secretValues := make([]any, 0)
	return RedactionReport{Value: redact(value, 0, "", &paths, &secretValues), Paths: paths, SecretValues: secretValues}
}

func redact(value any, depth int, path string, paths *[]string, secretValues *[]any) any {
	if depth >= maxRedactDepth {
		*paths = append(*paths, path)
		*secretValues = append(*secretValues, cloneRedactionValue(value, 0))
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		copyMap := make(map[string]any, len(typed))
		for _, key := range sortedMapKeys(typed) {
			item := typed[key]
			childPath := jsonPointerChild(path, key)
			if isSensitiveKey(key) {
				copyMap[key] = redactedValue
				*paths = append(*paths, childPath)
				*secretValues = append(*secretValues, cloneRedactionValue(item, 0))
			} else {
				copyMap[key] = redact(item, depth+1, childPath, paths, secretValues)
			}
		}
		return copyMap
	case map[string]string:
		copyMap := make(map[string]string, len(typed))
		for _, key := range sortedMapKeys(typed) {
			item := typed[key]
			if isSensitiveKey(key) {
				copyMap[key] = redactedValue
				*paths = append(*paths, jsonPointerChild(path, key))
				*secretValues = append(*secretValues, item)
			} else {
				copyMap[key] = item
			}
		}
		return copyMap
	case http.Header:
		copyHeader := make(http.Header, len(typed))
		for _, key := range sortedMapKeys(typed) {
			values := typed[key]
			if isSensitiveKey(key) {
				copyHeader[key] = []string{redactedValue}
				*paths = append(*paths, jsonPointerChild(path, key))
				*secretValues = append(*secretValues, append([]string(nil), values...))
			} else {
				copyHeader[key] = append([]string(nil), values...)
			}
		}
		return copyHeader
	case map[string][]string:
		copyMap := make(map[string][]string, len(typed))
		for _, key := range sortedMapKeys(typed) {
			values := typed[key]
			if isSensitiveKey(key) {
				copyMap[key] = []string{redactedValue}
				*paths = append(*paths, jsonPointerChild(path, key))
				*secretValues = append(*secretValues, append([]string(nil), values...))
			} else {
				copyMap[key] = append([]string(nil), values...)
			}
		}
		return copyMap
	case []any:
		copySlice := make([]any, len(typed))
		for index, item := range typed {
			copySlice[index] = redact(item, depth+1, jsonPointerChild(path, strconv.Itoa(index)), paths, secretValues)
		}
		return copySlice
	default:
		reflected := reflect.ValueOf(value)
		if !reflected.IsValid() {
			return nil
		}
		switch reflected.Kind() {
		case reflect.Map:
			if reflected.Type().Key().Kind() != reflect.String {
				return value
			}
			copyMap := make(map[string]any, reflected.Len())
			keys := reflected.MapKeys()
			sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
			for _, reflectedKey := range keys {
				key := reflectedKey.String()
				childPath := jsonPointerChild(path, key)
				if isSensitiveKey(key) {
					copyMap[key] = redactedValue
					*paths = append(*paths, childPath)
					*secretValues = append(*secretValues, cloneRedactionValue(reflected.MapIndex(reflectedKey).Interface(), 0))
				} else {
					copyMap[key] = redact(reflected.MapIndex(reflectedKey).Interface(), depth+1, childPath, paths, secretValues)
				}
			}
			return copyMap
		case reflect.Slice, reflect.Array:
			copySlice := make([]any, reflected.Len())
			for index := range reflected.Len() {
				copySlice[index] = redact(reflected.Index(index).Interface(), depth+1, jsonPointerChild(path, strconv.Itoa(index)), paths, secretValues)
			}
			return copySlice
		default:
			return value
		}
	}
}

type SecretRedactor struct {
	fingerprints map[string]struct{}
}

func NewSecretRedactor(secretValues []any) *SecretRedactor {
	redactor := &SecretRedactor{fingerprints: make(map[string]struct{})}
	for _, value := range secretValues {
		redactor.addFingerprints(value, 0)
	}
	return redactor
}

func (redactor *SecretRedactor) RedactWithReport(value any) RedactionReport {
	paths := make([]string, 0)
	return RedactionReport{Value: redactor.redact(value, 0, "", &paths), Paths: paths}
}

func (redactor *SecretRedactor) redact(value any, depth int, path string, paths *[]string) any {
	if redactor.matches(value) {
		appendUniquePath(paths, path)
		return redactedValue
	}
	if depth >= maxRedactDepth {
		appendUniquePath(paths, path)
		return redactedValue
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return value
		}
		copyMap := make(map[string]any, reflected.Len())
		keys := reflected.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		for _, reflectedKey := range keys {
			key := reflectedKey.String()
			if !redactor.matches(key) {
				copyMap[key] = nil
			}
		}
		for _, reflectedKey := range keys {
			key := reflectedKey.String()
			safeKey := key
			if redactor.matches(key) {
				safeKey = nextRedactedKey(copyMap)
			}
			childPath := jsonPointerChild(path, safeKey)
			if safeKey != key {
				appendUniquePath(paths, childPath)
			}
			copyMap[safeKey] = redactor.redact(reflected.MapIndex(reflectedKey).Interface(), depth+1, childPath, paths)
		}
		return copyMap
	case reflect.Slice, reflect.Array:
		copySlice := make([]any, reflected.Len())
		for index := range reflected.Len() {
			copySlice[index] = redactor.redact(reflected.Index(index).Interface(), depth+1, jsonPointerChild(path, strconv.Itoa(index)), paths)
		}
		return copySlice
	default:
		return value
	}
}

func (redactor *SecretRedactor) addFingerprints(value any, depth int) {
	if depth >= maxRedactDepth || value == nil {
		return
	}
	if text, ok := value.(string); ok && text == "" {
		return
	}
	if fingerprint, ok := canonicalFingerprint(value); ok {
		redactor.fingerprints[fingerprint] = struct{}{}
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return
	}
	switch reflected.Kind() {
	case reflect.Map:
		for _, key := range reflected.MapKeys() {
			redactor.addFingerprints(reflected.MapIndex(key).Interface(), depth+1)
		}
	case reflect.Slice, reflect.Array:
		for index := range reflected.Len() {
			redactor.addFingerprints(reflected.Index(index).Interface(), depth+1)
		}
	}
}

func (redactor *SecretRedactor) matches(value any) bool {
	if redactor == nil || len(redactor.fingerprints) == 0 || value == nil {
		return false
	}
	if text, ok := value.(string); ok && text == "" {
		return false
	}
	fingerprint, ok := canonicalFingerprint(value)
	if !ok {
		return false
	}
	_, exists := redactor.fingerprints[fingerprint]
	return exists
}

func canonicalFingerprint(value any) (string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func cloneRedactionValue(value any, depth int) any {
	if depth >= maxRedactDepth || value == nil {
		return value
	}
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return value
		}
		copyMap := make(map[string]any, reflected.Len())
		for _, key := range reflected.MapKeys() {
			copyMap[key.String()] = cloneRedactionValue(reflected.MapIndex(key).Interface(), depth+1)
		}
		return copyMap
	case reflect.Slice, reflect.Array:
		copySlice := make([]any, reflected.Len())
		for index := range reflected.Len() {
			copySlice[index] = cloneRedactionValue(reflected.Index(index).Interface(), depth+1)
		}
		return copySlice
	default:
		return value
	}
}

func nextRedactedKey(values map[string]any) string {
	if _, exists := values[redactedValue]; !exists {
		return redactedValue
	}
	for suffix := 2; ; suffix++ {
		candidate := redactedValue + "#" + strconv.Itoa(suffix)
		if _, exists := values[candidate]; !exists {
			return candidate
		}
	}
}

func appendUniquePath(paths *[]string, path string) {
	for _, existing := range *paths {
		if existing == path {
			return
		}
	}
	*paths = append(*paths, path)
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func jsonPointerChild(path, token string) string {
	return path + "/" + escapeJSONPointerToken(token)
}

func escapeJSONPointerToken(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	for _, marker := range []string{"authorization", "cookie", "token", "secret", "password", "apikey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
