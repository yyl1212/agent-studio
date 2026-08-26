package domain

import (
	"encoding/json"
	"time"
)

type WorkflowSnapshotKind string

const (
	WorkflowSnapshotDraft   WorkflowSnapshotKind = "draft"
	WorkflowSnapshotVersion WorkflowSnapshotKind = "version"
)

type WorkflowSnapshotRef struct {
	Kind          WorkflowSnapshotKind `json:"kind"`
	Version       *int                 `json:"version,omitempty"`
	DraftRevision *int64               `json:"draftRevision,omitempty"`
}

type WorkflowSnapshotDescriptor struct {
	Kind          WorkflowSnapshotKind `json:"kind"`
	Version       *int                 `json:"version,omitempty"`
	VersionID     *string              `json:"versionId,omitempty"`
	DraftRevision *int64               `json:"draftRevision,omitempty"`
	CreatedAt     *time.Time           `json:"createdAt,omitempty"`
}

type WorkflowVersionSummary struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Current   bool      `json:"current"`
	CreatedAt time.Time `json:"createdAt"`
}

type RollbackCheckpointSummary struct {
	SourceRevision      int64     `json:"sourceRevision"`
	RestoredRevision    int64     `json:"restoredRevision"`
	RestoredFromVersion int       `json:"restoredFromVersion"`
	CreatedAt           time.Time `json:"createdAt"`
}

type WorkflowVersionPage struct {
	Items              []WorkflowVersionSummary   `json:"items"`
	NextCursor         *string                    `json:"nextCursor"`
	RollbackCheckpoint *RollbackCheckpointSummary `json:"rollbackCheckpoint"`
}

type WorkflowDiffKind string
type WorkflowDiffValueOmission string

const (
	WorkflowDiffAdded     WorkflowDiffKind = "added"
	WorkflowDiffRemoved   WorkflowDiffKind = "removed"
	WorkflowDiffModified  WorkflowDiffKind = "modified"
	WorkflowDiffReordered WorkflowDiffKind = "reordered"

	WorkflowDiffSecret                WorkflowDiffValueOmission = "secret"
	WorkflowDiffDefinitionUnavailable WorkflowDiffValueOmission = "definition_unavailable"
	WorkflowDiffTooLarge              WorkflowDiffValueOmission = "too_large"
)

type WorkflowValueDiff struct {
	Path         string                     `json:"path"`
	Kind         WorkflowDiffKind           `json:"kind"`
	Before       *json.RawMessage           `json:"before,omitempty"`
	After        *json.RawMessage           `json:"after,omitempty"`
	ValueOmitted *WorkflowDiffValueOmission `json:"valueOmitted,omitempty"`
}

type WorkflowNodeTypeSummary struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Title   string `json:"title"`
}

type WorkflowNodeDiff struct {
	NodeID     string                   `json:"nodeId"`
	Title      string                   `json:"title"`
	Kind       WorkflowDiffKind         `json:"kind"`
	BeforeType *WorkflowNodeTypeSummary `json:"beforeType,omitempty"`
	AfterType  *WorkflowNodeTypeSummary `json:"afterType,omitempty"`
	Config     []WorkflowValueDiff      `json:"config"`
}

type WorkflowStartParameterDiff struct {
	Key         string              `json:"key"`
	Kind        WorkflowDiffKind    `json:"kind"`
	BeforeOrder *int                `json:"beforeOrder,omitempty"`
	AfterOrder  *int                `json:"afterOrder,omitempty"`
	Changes     []WorkflowValueDiff `json:"changes"`
}

type WorkflowConnectionSummary struct {
	Source     string `json:"source"`
	SourcePort string `json:"sourcePort"`
	Target     string `json:"target"`
	TargetPort string `json:"targetPort"`
}

type WorkflowConnectionDiff struct {
	Kind       WorkflowDiffKind          `json:"kind"`
	Connection WorkflowConnectionSummary `json:"connection"`
}

type WorkflowPresentationDiff struct {
	Field  string            `json:"field"`
	Change WorkflowValueDiff `json:"change"`
}

type WorkflowLayoutDiff struct {
	NodeID string   `json:"nodeId"`
	Title  string   `json:"title"`
	Before Position `json:"before"`
	After  Position `json:"after"`
}

type WorkflowDiffSummary struct {
	Total             int `json:"total"`
	Nodes             int `json:"nodes"`
	StartParameters   int `json:"startParameters"`
	Connections       int `json:"connections"`
	AgentPresentation int `json:"agentPresentation"`
	Layout            int `json:"layout"`
}

type WorkflowDiffGroups struct {
	Nodes             []WorkflowNodeDiff           `json:"nodes"`
	StartParameters   []WorkflowStartParameterDiff `json:"startParameters"`
	Connections       []WorkflowConnectionDiff     `json:"connections"`
	AgentPresentation []WorkflowPresentationDiff   `json:"agentPresentation"`
	Layout            []WorkflowLayoutDiff         `json:"layout"`
}

type WorkflowDiff struct {
	Base      WorkflowSnapshotDescriptor `json:"base"`
	Compare   WorkflowSnapshotDescriptor `json:"compare"`
	Summary   WorkflowDiffSummary        `json:"summary"`
	Truncated bool                       `json:"truncated"`
	Groups    WorkflowDiffGroups         `json:"groups"`
}
