package nodesecurity

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type Source string

const (
	SourceSchema       Source = "schema"
	SourceSensitiveKey Source = "sensitive_key"
	SourceHTTPURL      Source = "http_url"
	SourceHTTPHeader   Source = "http_header"
)

type Match struct {
	Pointer    string
	LegacyPath string
	Source     Source
	HasValue   bool
}

func InspectConfig(nodeType, nodeVersion string, config, schema json.RawMessage) ([]Match, error) {
	value, err := decodeSingleJSON(config)
	if err != nil {
		return nil, fmt.Errorf("decode node config: %w", err)
	}
	matches := inspectSensitiveKeys(value, "", "config")
	schemaMatches, err := inspectSchema(value, schema)
	if err != nil {
		return nil, fmt.Errorf("decode node config schema: %w", err)
	}
	matches = append(matches, schemaMatches...)
	if nodeType == "http" && nodeVersion == "1" {
		matches = append(matches, inspectHTTP(value)...)
	}
	return normalizeMatches(matches), nil
}

func decodeSingleJSON(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func inspectSensitiveKeys(value any, pointer, legacyPath string) []Match {
	matches := make([]Match, 0)
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedKeys(typed) {
			childPointer := joinPointer(pointer, key)
			childLegacyPath := legacyPath + "." + key
			if sensitiveConfigKey(key) && hasCredentialValue(typed[key]) {
				matches = append(matches, Match{
					Pointer: childPointer, LegacyPath: childLegacyPath,
					Source: SourceSensitiveKey, HasValue: true,
				})
				continue
			}
			matches = append(matches, inspectSensitiveKeys(typed[key], childPointer, childLegacyPath)...)
		}
	case []any:
		for index, child := range typed {
			matches = append(matches, inspectSensitiveKeys(
				child,
				joinPointer(pointer, strconv.Itoa(index)),
				legacyPath+"["+strconv.Itoa(index)+"]",
			)...)
		}
	}
	return matches
}

func inspectSchema(config any, rawSchema json.RawMessage) ([]Match, error) {
	if len(bytes.TrimSpace(rawSchema)) == 0 {
		return nil, nil
	}
	root, err := decodeSingleJSON(rawSchema)
	if err != nil {
		return nil, err
	}
	return walkSchema(config, root, root, "", "config", map[string]bool{}), nil
}

func walkSchema(config, schema, root any, pointer, legacyPath string, visiting map[string]bool) []Match {
	schemaObject, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	matches := make([]Match, 0)
	writeOnly, _ := schemaObject["writeOnly"].(bool)
	agentStudioSecret, _ := schemaObject["x-agent-studio-secret"].(bool)
	if writeOnly || agentStudioSecret {
		matches = append(matches, Match{
			Pointer: pointer, LegacyPath: legacyPath,
			Source: SourceSchema, HasValue: hasCredentialValue(config),
		})
	}
	if reference, _ := schemaObject["$ref"].(string); strings.HasPrefix(reference, "#") && !visiting[reference] {
		if resolved, found := resolveLocalReference(root, reference); found {
			nextVisiting := cloneVisited(visiting)
			nextVisiting[reference] = true
			matches = append(matches, walkSchema(config, resolved, root, pointer, legacyPath, nextVisiting)...)
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		branches, ok := schemaObject[keyword].([]any)
		if !ok {
			continue
		}
		for _, branch := range branches {
			matches = append(matches, walkSchema(config, branch, root, pointer, legacyPath, visiting)...)
		}
	}
	if properties, ok := schemaObject["properties"].(map[string]any); ok {
		if configObject, ok := config.(map[string]any); ok {
			for _, key := range sortedKeys(configObject) {
				propertySchema, exists := properties[key]
				if !exists {
					continue
				}
				matches = append(matches, walkSchema(
					configObject[key], propertySchema, root,
					joinPointer(pointer, key), legacyPath+"."+key, visiting,
				)...)
			}
		}
	}
	if itemSchema, ok := schemaObject["items"]; ok {
		if configItems, ok := config.([]any); ok {
			for index, item := range configItems {
				matches = append(matches, walkSchema(
					item, itemSchema, root,
					joinPointer(pointer, strconv.Itoa(index)),
					legacyPath+"["+strconv.Itoa(index)+"]", visiting,
				)...)
			}
		}
	}
	return matches
}

func resolveLocalReference(root any, reference string) (any, bool) {
	if reference == "#" {
		return root, true
	}
	if !strings.HasPrefix(reference, "#/") {
		return nil, false
	}
	current := root
	for _, encodedToken := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		token := strings.ReplaceAll(strings.ReplaceAll(encodedToken, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func inspectHTTP(value any) []Match {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	matches := make([]Match, 0)
	if rawURL, ok := object["url"].(string); ok && hasSensitiveQuery(rawURL) {
		matches = append(matches, Match{
			Pointer: "/url", LegacyPath: "config.url", Source: SourceHTTPURL, HasValue: true,
		})
	}
	if headers, ok := object["headers"].([]any); ok {
		for index, rawHeader := range headers {
			header, ok := rawHeader.(map[string]any)
			if !ok {
				continue
			}
			name, _ := header["name"].(string)
			valueSource, _ := header["valueSource"].(string)
			if sensitiveHeaderName(name) && valueSource == "literal" && hasCredentialValue(header["value"]) {
				matches = append(matches, Match{
					Pointer:    "/headers/" + strconv.Itoa(index) + "/value",
					LegacyPath: "config.headers[" + strconv.Itoa(index) + "].value",
					Source:     SourceHTTPHeader, HasValue: true,
				})
			}
		}
	}
	return matches
}

func hasSensitiveQuery(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for key, values := range parsed.Query() {
		if !sensitiveConfigKey(key) {
			continue
		}
		for _, value := range values {
			if value != "" {
				return true
			}
		}
	}
	return false
}

func sensitiveHeaderName(name string) bool {
	return sensitiveConfigKey(name) || strings.EqualFold(strings.TrimSpace(name), "proxy-authorization")
}

func sensitiveConfigKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range []string{"apikey", "password", "authorization", "cookie", "secret"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "token" || strings.Contains(normalized, "accesstoken") ||
		strings.Contains(normalized, "refreshtoken") || strings.Contains(normalized, "authtoken") ||
		strings.Contains(normalized, "bearertoken") || strings.Contains(normalized, "apitoken")
}

func hasCredentialValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func normalizeMatches(matches []Match) []Match {
	sort.Slice(matches, func(i, j int) bool {
		left, right := matches[i], matches[j]
		if left.Pointer != right.Pointer {
			return left.Pointer < right.Pointer
		}
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		if left.HasValue != right.HasValue {
			return !left.HasValue
		}
		return left.LegacyPath < right.LegacyPath
	})
	unique := matches[:0]
	for _, match := range matches {
		if len(unique) > 0 {
			previous := unique[len(unique)-1]
			if previous.Pointer == match.Pointer && previous.Source == match.Source && previous.HasValue == match.HasValue {
				continue
			}
		}
		unique = append(unique, match)
	}
	return unique
}

func joinPointer(parent, token string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
	return parent + "/" + escaped
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cloneVisited(input map[string]bool) map[string]bool {
	output := make(map[string]bool, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}
