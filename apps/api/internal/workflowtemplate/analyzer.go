package workflowtemplate

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/apps/api/internal/engine"
	"github.com/yyl1212/agent-studio/apps/api/internal/nodes/builtin"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

type Compiler interface {
	Compile(domain.Graph) (*engine.Plan, []domain.ValidationIssue)
}

type NodeCatalog interface {
	Definitions() []agentnode.Definition
}

type Analyzer struct {
	compiler Compiler
	catalog  NodeCatalog
}

func NewAnalyzer(compiler Compiler, catalog NodeCatalog) *Analyzer {
	return &Analyzer{compiler: compiler, catalog: catalog}
}

func (analyzer *Analyzer) Analyze(input Template) Analysis {
	input.Metadata.Name = strings.TrimSpace(input.Metadata.Name)
	definitions := definitionIndex(analyzer.catalog.Definitions())
	preview := Preview{
		Metadata: input.Metadata,
		Summary: Summary{
			NodeCount:   len(input.Spec.Graph.Nodes),
			EdgeCount:   len(input.Spec.Graph.Edges),
			InputSchema: json.RawMessage(`{}`),
			NodeTypes:   []NodeTypeSummary{},
		},
		Issues: []domain.ValidationIssue{},
	}
	preview.Issues = append(preview.Issues, envelopeIssues(input)...)
	preview.Issues = append(preview.Issues, budgetIssues(input.Spec.Graph)...)
	if len(preview.Issues) == 0 {
		preview.Summary.NodeTypes = summarizeNodeTypes(input.Spec.Graph, definitions)
		preview.Issues = append(preview.Issues, securityIssues(input.Spec.Graph, definitions)...)
	}
	if len(preview.Issues) == 0 {
		plan, issues := analyzer.compiler.Compile(input.Spec.Graph)
		preview.Issues = append(preview.Issues, issues...)
		if len(issues) == 0 {
			start := plan.Nodes[plan.StartNodeID].Node
			schema, err := builtin.DeriveInputSchema(start.Config)
			if err != nil {
				preview.Issues = append(preview.Issues, templateIssue("NODE_CONFIG_INVALID", "开始节点配置不符合 Schema", start.ID, "config"))
			} else {
				preview.Summary.InputSchema = schema
			}
		}
	}
	preview.Issues = sortTemplateIssues(preview.Issues)
	preview.Valid = len(preview.Issues) == 0
	analysis := Analysis{Preview: preview}
	if !preview.Valid {
		return analysis
	}

	normalized, err := Canonicalize(input)
	if err != nil {
		preview.Valid = false
		preview.Issues = sortTemplateIssues(append(preview.Issues, templateIssue("TEMPLATE_CONFIG_JSON_INVALID", "节点配置 JSON 无效", "", "spec.graph")))
		analysis.Preview = preview
		return analysis
	}
	encoded, err := Encode(normalized)
	if err != nil || len(encoded) > MaxTemplateBytes {
		preview.Valid = false
		preview.Issues = sortTemplateIssues(append(preview.Issues, templateIssue("TEMPLATE_LIMIT_EXCEEDED", "模板文件超过 2 MiB", "", "template")))
		analysis.Preview = preview
		return analysis
	}
	analysis.Normalized = normalized
	return analysis
}

func envelopeIssues(template Template) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0)
	for _, path := range template.missingRequired {
		issues = append(issues, templateIssue("TEMPLATE_FIELD_REQUIRED", "工作流模板缺少必填字段", "", path))
	}
	if template.APIVersion != APIVersion {
		issues = append(issues, templateIssue("TEMPLATE_API_VERSION_UNSUPPORTED", "工作流模板 apiVersion 不受支持", "", "apiVersion"))
	}
	if template.Kind != Kind {
		issues = append(issues, templateIssue("TEMPLATE_KIND_INVALID", "工作流模板 kind 无效", "", "kind"))
	}
	nameLength := utf8.RuneCountInString(template.Metadata.Name)
	if nameLength == 0 || nameLength > 128 {
		issues = append(issues, templateIssue("TEMPLATE_METADATA_INVALID", "模板名称必须为 1 至 128 个字符", "", "metadata.name"))
	}
	if utf8.RuneCountInString(template.Metadata.Description) > 2048 {
		issues = append(issues, templateIssue("TEMPLATE_METADATA_INVALID", "模板说明不能超过 2048 个字符", "", "metadata.description"))
	}
	return issues
}

func budgetIssues(graph domain.Graph) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0, 2)
	if len(graph.Nodes) > MaxNodes {
		issues = append(issues, templateIssue("TEMPLATE_LIMIT_EXCEEDED", "模板节点数量超过限制", "", "spec.graph.nodes"))
	}
	if len(graph.Edges) > MaxEdges {
		issues = append(issues, templateIssue("TEMPLATE_LIMIT_EXCEEDED", "模板连线数量超过限制", "", "spec.graph.edges"))
	}
	return issues
}

func summarizeNodeTypes(graph domain.Graph, definitions map[string]agentnode.Definition) []NodeTypeSummary {
	type summaryKey struct {
		nodeType string
		version  string
	}
	counts := make(map[summaryKey]int)
	for _, node := range graph.Nodes {
		counts[summaryKey{nodeType: node.Type, version: node.TypeVersion}]++
	}
	keys := make([]summaryKey, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].nodeType != keys[j].nodeType {
			return keys[i].nodeType < keys[j].nodeType
		}
		return keys[i].version < keys[j].version
	})
	summaries := make([]NodeTypeSummary, 0, len(keys))
	for _, key := range keys {
		definition, available := definitions[key.nodeType+"@"+key.version]
		title := key.nodeType
		capabilities := []agentnode.Capability{}
		if available {
			title = definition.Title
			capabilities = append(capabilities, definition.Capabilities...)
		}
		summaries = append(summaries, NodeTypeSummary{
			Type:         key.nodeType,
			Version:      key.version,
			Title:        title,
			Count:        counts[key],
			Available:    available,
			Capabilities: capabilities,
		})
	}
	return summaries
}

func sortTemplateIssues(issues []domain.ValidationIssue) []domain.ValidationIssue {
	sort.Slice(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.NodeID != right.NodeID {
			return left.NodeID < right.NodeID
		}
		if left.EdgeID != right.EdgeID {
			return left.EdgeID < right.EdgeID
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Message < right.Message
	})
	unique := issues[:0]
	for _, issue := range issues {
		if len(unique) > 0 && unique[len(unique)-1] == issue {
			continue
		}
		unique = append(unique, issue)
	}
	return unique
}
