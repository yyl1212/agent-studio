package workflow

import (
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
	Value any
	Paths []string
}

func RedactWithReport(value any) RedactionReport {
	paths := make([]string, 0)
	return RedactionReport{Value: redact(value, 0, "", &paths), Paths: paths}
}

func redact(value any, depth int, path string, paths *[]string) any {
	if depth >= maxRedactDepth {
		*paths = append(*paths, path)
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
			} else {
				copyMap[key] = redact(item, depth+1, childPath, paths)
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
			} else {
				copyMap[key] = append([]string(nil), values...)
			}
		}
		return copyMap
	case []any:
		copySlice := make([]any, len(typed))
		for index, item := range typed {
			copySlice[index] = redact(item, depth+1, jsonPointerChild(path, strconv.Itoa(index)), paths)
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
				} else {
					copyMap[key] = redact(reflected.MapIndex(reflectedKey).Interface(), depth+1, childPath, paths)
				}
			}
			return copyMap
		case reflect.Slice, reflect.Array:
			copySlice := make([]any, reflected.Len())
			for index := range reflected.Len() {
				copySlice[index] = redact(reflected.Index(index).Interface(), depth+1, jsonPointerChild(path, strconv.Itoa(index)), paths)
			}
			return copySlice
		default:
			return value
		}
	}
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
