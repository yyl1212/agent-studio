package workflow

import (
	"net/http"
	"strings"
)

const redactedValue = "[REDACTED]"

func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copyMap := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				copyMap[key] = redactedValue
			} else {
				copyMap[key] = Redact(item)
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
			copySlice[index] = Redact(item)
		}
		return copySlice
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "api-key", "apikey", "token", "secret":
		return true
	default:
		return false
	}
}
