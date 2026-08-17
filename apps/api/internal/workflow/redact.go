package workflow

import (
	"net/http"
	"reflect"
	"strings"
)

const redactedValue = "[REDACTED]"
const maxRedactDepth = 64

func Redact(value any) any {
	return redact(value, 0)
}

func redact(value any, depth int) any {
	if depth >= maxRedactDepth {
		return redactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		copyMap := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				copyMap[key] = redactedValue
			} else {
				copyMap[key] = redact(item, depth+1)
			}
		}
		return copyMap
	case map[string]string:
		copyMap := make(map[string]string, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				copyMap[key] = redactedValue
			} else {
				copyMap[key] = item
			}
		}
		return copyMap
	case http.Header:
		copyHeader := make(http.Header, len(typed))
		for key, values := range typed {
			if isSensitiveKey(key) {
				copyHeader[key] = []string{redactedValue}
			} else {
				copyHeader[key] = append([]string(nil), values...)
			}
		}
		return copyHeader
	case map[string][]string:
		copyMap := make(map[string][]string, len(typed))
		for key, values := range typed {
			if isSensitiveKey(key) {
				copyMap[key] = []string{redactedValue}
			} else {
				copyMap[key] = append([]string(nil), values...)
			}
		}
		return copyMap
	case []any:
		copySlice := make([]any, len(typed))
		for index, item := range typed {
			copySlice[index] = redact(item, depth+1)
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
			iterator := reflected.MapRange()
			for iterator.Next() {
				key := iterator.Key().String()
				if isSensitiveKey(key) {
					copyMap[key] = redactedValue
				} else {
					copyMap[key] = redact(iterator.Value().Interface(), depth+1)
				}
			}
			return copyMap
		case reflect.Slice, reflect.Array:
			copySlice := make([]any, reflected.Len())
			for index := range reflected.Len() {
				copySlice[index] = redact(reflected.Index(index).Interface(), depth+1)
			}
			return copySlice
		default:
			return value
		}
	}
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
