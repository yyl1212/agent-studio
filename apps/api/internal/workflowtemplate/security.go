package workflowtemplate

import (
	"encoding/json"
	"sort"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodesecurity"
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
		rawSchema := json.RawMessage(nil)
		if definition, available := definitions[node.Type+"@"+node.TypeVersion]; available {
			rawSchema = definition.ConfigSchema
		}
		matches, err := nodesecurity.InspectConfig(node.Type, node.TypeVersion, node.Config, rawSchema)
		if err != nil {
			issues = append(issues, templateIssue("TEMPLATE_CONFIG_JSON_INVALID", "节点配置 JSON 无效", node.ID, "config"))
			continue
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].LegacyPath != matches[j].LegacyPath {
				return matches[i].LegacyPath < matches[j].LegacyPath
			}
			return templateSecretSourcePriority(matches[i].Source) < templateSecretSourcePriority(matches[j].Source)
		})
		for _, match := range matches {
			if !match.HasValue {
				continue
			}
			message := "节点配置包含不允许导出的凭据字段"
			if match.Source == nodesecurity.SourceSchema {
				message = "节点配置包含 Schema 标记的只写凭据"
			} else if match.Source == nodesecurity.SourceHTTPURL {
				message = "HTTP URL 包含不允许导出的凭据参数"
			} else if match.Source == nodesecurity.SourceHTTPHeader {
				message = "HTTP Header 包含不允许导出的明文凭据"
			}
			issues = append(issues, templateIssue("TEMPLATE_SECRET_CONFIG_FOUND", message, node.ID, match.LegacyPath))
		}
	}
	return deduplicateIssues(issues)
}

func templateSecretSourcePriority(source nodesecurity.Source) int {
	switch source {
	case nodesecurity.SourceSensitiveKey:
		return 0
	case nodesecurity.SourceSchema:
		return 1
	case nodesecurity.SourceHTTPURL, nodesecurity.SourceHTTPHeader:
		return 2
	default:
		return 3
	}
}

func configDepth(raw json.RawMessage) (int, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
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

func deduplicateIssues(issues []domain.ValidationIssue) []domain.ValidationIssue {
	sort.SliceStable(issues, func(i, j int) bool {
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
