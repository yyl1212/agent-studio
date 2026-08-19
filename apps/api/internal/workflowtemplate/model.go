package workflowtemplate

import (
	"encoding/json"

	"github.com/yyl1212/agent-studio/apps/api/internal/domain"
	"github.com/yyl1212/agent-studio/sdk/go/agentnode"
)

const (
	APIVersionV1Alpha1 = "agent-studio.dev/v1alpha1"
	APIVersionV1Alpha2 = "agent-studio.dev/v1alpha2"
	APIVersion         = APIVersionV1Alpha2
	Kind               = "WorkflowTemplate"
	MaxNodes           = 500
	MaxEdges           = 2000
	MaxNodePackages    = 128
	MaxPackageNodes    = 512
	MaxDepth           = 64
	MaxTemplateBytes   = 2 << 20
)

type Template struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`

	missingRequired []string
}

type Metadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Spec struct {
	NodePackages []NodePackageRequirement `json:"nodePackages"`
	Graph        domain.Graph             `json:"graph"`
}

type NodePackageRequirement struct {
	Name    string            `json:"name"`
	Version string            `json:"version,omitempty"`
	Nodes   []NodePackageNode `json:"nodes"`
}

type NodePackageNode struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

type Preview struct {
	Valid    bool                     `json:"valid"`
	Metadata Metadata                 `json:"metadata"`
	Summary  Summary                  `json:"summary"`
	Issues   []domain.ValidationIssue `json:"issues"`
}

type Summary struct {
	NodeCount   int               `json:"nodeCount"`
	EdgeCount   int               `json:"edgeCount"`
	InputSchema json.RawMessage   `json:"inputSchema"`
	NodeTypes   []NodeTypeSummary `json:"nodeTypes"`
}

type NodeTypeSummary struct {
	Type         string                 `json:"type"`
	Version      string                 `json:"version"`
	Title        string                 `json:"title"`
	Count        int                    `json:"count"`
	Available    bool                   `json:"available"`
	Capabilities []agentnode.Capability `json:"capabilities"`
}

type Analysis struct {
	Normalized Template
	Preview    Preview
}
