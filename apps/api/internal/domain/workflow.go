package domain

import (
	"encoding/json"
	"time"
)

type AgentAccent string

const (
	AgentAccentIndigo AgentAccent = "indigo"
	AgentAccentBlue   AgentAccent = "blue"
	AgentAccentTeal   AgentAccent = "teal"
	AgentAccentAmber  AgentAccent = "amber"
	AgentAccentRose   AgentAccent = "rose"
)

type AgentResultMode string

const (
	AgentResultModeAuto AgentResultMode = "auto"
	AgentResultModeText AgentResultMode = "text"
	AgentResultModeJSON AgentResultMode = "json"
)

type AgentPresentation struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Accent      AgentAccent     `json:"accent"`
	SubmitLabel string          `json:"submitLabel"`
	ResultMode  AgentResultMode `json:"resultMode"`
}

type Workflow struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Slug               string            `json:"slug"`
	Description        string            `json:"description"`
	AgentPresentation  AgentPresentation `json:"agentPresentation"`
	DraftGraph         json.RawMessage   `json:"draftGraph"`
	DraftRevision      int64             `json:"draftRevision"`
	PublishedVersionID *string           `json:"publishedVersionId,omitempty"`
	PublishedVersion   *int              `json:"publishedVersion,omitempty"`
	ArchivedAt         *time.Time        `json:"archivedAt,omitempty"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
}

type WorkflowSummary struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Slug               string     `json:"slug"`
	Description        string     `json:"description"`
	DraftRevision      int64      `json:"draftRevision"`
	PublishedVersionID *string    `json:"publishedVersionId,omitempty"`
	PublishedVersion   *int       `json:"publishedVersion,omitempty"`
	ArchivedAt         *time.Time `json:"archivedAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type WorkflowVersion struct {
	ID                string            `json:"id"`
	WorkflowID        string            `json:"workflowId"`
	Version           int               `json:"version"`
	Graph             json.RawMessage   `json:"graph"`
	InputSchema       json.RawMessage   `json:"inputSchema"`
	AgentPresentation AgentPresentation `json:"agentPresentation"`
	CreatedAt         time.Time         `json:"createdAt"`
}
