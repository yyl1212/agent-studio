package workflowtemplate

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

func definitionIndex(definitions []agentnode.Definition) map[string]agentnode.Definition {
	indexed := make(map[string]agentnode.Definition, len(definitions))
	for _, definition := range definitions {
		indexed[definition.Type+"@"+definition.Version] = definition
	}
	return indexed
}

func securityIssues(graph domain.Graph, definitions map[string]agentnode.Definition) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0)
	for _, node := range graph.Nodes {
		depth, err := configDepth(node.Config)
		if err != nil {
			issues = append(issues, templateIssue("TEMPLATE_CONFIG_JSON_INVALID", "节点配置 JSON 无效", node.ID, "config"))
			continue
		}
		if depth > MaxDepth {
			issues = append(issues, templateIssue("TEMPLATE_LIMIT_EXCEEDED", "节点配置嵌套层级超过限制", node.ID, "config"))
			continue
		}
		var config any
		if err := json.Unmarshal(node.Config, &config); err != nil {
			issues = append(issues, templateIssue("TEMPLATE_CONFIG_JSON_INVALID", "节点配置 JSON 无效", node.ID, "config"))
			continue
		}
		issues = append(issues, sensitiveValueIssues(node.ID, "config", config)...)
		if definition, ok := definitions[node.Type+"@"+node.TypeVersion]; ok {
			issues = append(issues, schemaSecretIssues(node.ID, config, definition.ConfigSchema)...)
		}
		if node.Type == "http" && node.TypeVersion == "1" {
			issues = append(issues, httpSecretIssues(node.ID, config)...)
		}
	}
	return deduplicateIssues(issues)
}

func configDepth(raw json.RawMessage) (int, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return valueDepth(value, 1), nil
}

func valueDepth(value any, depth int) int {
	maximum := depth
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			maximum = max(maximum, valueDepth(child, depth+1))
		}
	case []any:
		for _, child := range typed {
			maximum = max(maximum, valueDepth(child, depth+1))
		}
	}
	return maximum
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

func sensitiveValueIssues(nodeID, path string, value any) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0)
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedKeys(typed)
		for _, key := range keys {
			childPath := joinObjectPath(path, key)
			if sensitiveConfigKey(key) && hasCredentialValue(typed[key]) {
				issues = append(issues, templateIssue("TEMPLATE_SECRET_CONFIG_FOUND", "节点配置包含不允许导出的凭据字段", nodeID, childPath))
				continue
			}
			issues = append(issues, sensitiveValueIssues(nodeID, childPath, typed[key])...)
		}
	case []any:
		for index, child := range typed {
			issues = append(issues, sensitiveValueIssues(nodeID, path+"["+strconv.Itoa(index)+"]", child)...)
		}
	}
	return issues
}

func schemaSecretIssues(nodeID string, config any, rawSchema json.RawMessage) []domain.ValidationIssue {
	if len(rawSchema) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(rawSchema, &root); err != nil {
		return nil
	}
	return walkSchemaSecrets(nodeID, "config", config, root, root, map[string]bool{})
}

func walkSchemaSecrets(nodeID, path string, config, schema, root any, visiting map[string]bool) []domain.ValidationIssue {
	schemaObject, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	writeOnly, _ := schemaObject["writeOnly"].(bool)
	agentStudioSecret, _ := schemaObject["x-agent-studio-secret"].(bool)
	if (writeOnly || agentStudioSecret) && hasCredentialValue(config) {
		return []domain.ValidationIssue{templateIssue("TEMPLATE_SECRET_CONFIG_FOUND", "节点配置包含 Schema 标记的只写凭据", nodeID, path)}
	}
	issues := make([]domain.ValidationIssue, 0)
	if reference, _ := schemaObject["$ref"].(string); strings.HasPrefix(reference, "#") && !visiting[reference] {
		if resolved, ok := resolveLocalReference(root, reference); ok {
			nextVisiting := cloneVisited(visiting)
			nextVisiting[reference] = true
			issues = append(issues, walkSchemaSecrets(nodeID, path, config, resolved, root, nextVisiting)...)
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if branches, ok := schemaObject[keyword].([]any); ok {
			for _, branch := range branches {
				issues = append(issues, walkSchemaSecrets(nodeID, path, config, branch, root, visiting)...)
			}
		}
	}
	if properties, ok := schemaObject["properties"].(map[string]any); ok {
		if configObject, ok := config.(map[string]any); ok {
			keys := sortedKeys(configObject)
			for _, key := range keys {
				propertySchema, exists := properties[key]
				if !exists {
					continue
				}
				issues = append(issues, walkSchemaSecrets(nodeID, joinObjectPath(path, key), configObject[key], propertySchema, root, visiting)...)
			}
		}
	}
	if itemSchema, ok := schemaObject["items"]; ok {
		if configItems, ok := config.([]any); ok {
			for index, item := range configItems {
				issues = append(issues, walkSchemaSecrets(nodeID, path+"["+strconv.Itoa(index)+"]", item, itemSchema, root, visiting)...)
			}
		}
	}
	return issues
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

func httpSecretIssues(nodeID string, config any) []domain.ValidationIssue {
	object, ok := config.(map[string]any)
	if !ok {
		return nil
	}
	issues := make([]domain.ValidationIssue, 0)
	if rawURL, ok := object["url"].(string); ok && hasSensitiveQuery(rawURL) {
		issues = append(issues, templateIssue("TEMPLATE_SECRET_CONFIG_FOUND", "HTTP URL 包含不允许导出的凭据参数", nodeID, "config.url"))
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
				path := "config.headers[" + strconv.Itoa(index) + "].value"
				issues = append(issues, templateIssue("TEMPLATE_SECRET_CONFIG_FOUND", "HTTP Header 包含不允许导出的明文凭据", nodeID, path))
			}
		}
	}
	return issues
}

func hasSensitiveQuery(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for key, values := range parsed.Query() {
		if sensitiveConfigKey(key) && len(values) > 0 {
			for _, value := range values {
				if value != "" {
					return true
				}
			}
		}
	}
	return false
}

func sensitiveHeaderName(name string) bool {
	return sensitiveConfigKey(name) || strings.EqualFold(strings.TrimSpace(name), "proxy-authorization")
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

func deduplicateIssues(issues []domain.ValidationIssue) []domain.ValidationIssue {
	sort.Slice(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Code < right.Code
	})
	unique := issues[:0]
	for _, issue := range issues {
		if len(unique) > 0 {
			previous := unique[len(unique)-1]
			if previous.Code == issue.Code && previous.NodeID == issue.NodeID && previous.Path == issue.Path {
				continue
			}
		}
		unique = append(unique, issue)
	}
	return unique
}

func templateIssue(code, message, nodeID, path string) domain.ValidationIssue {
	return domain.ValidationIssue{Code: code, Message: message, NodeID: nodeID, Path: path}
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinObjectPath(parent, key string) string {
	return parent + "." + key
}

func cloneVisited(input map[string]bool) map[string]bool {
	output := make(map[string]bool, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}
