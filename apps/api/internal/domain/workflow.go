package domain

import (
	"encoding/json"
	"time"
)

type Workflow struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Description        string          `json:"description"`
	DraftGraph         json.RawMessage `json:"draftGraph"`
	DraftRevision      int64           `json:"draftRevision"`
	PublishedVersionID *string         `json:"publishedVersionId,omitempty"`
	PublishedVersion   *int            `json:"publishedVersion,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
}

type WorkflowVersion struct {
	ID          string          `json:"id"`
	WorkflowID  string          `json:"workflowId"`
	Version     int             `json:"version"`
	Graph       json.RawMessage `json:"graph"`
	InputSchema json.RawMessage `json:"inputSchema"`
	CreatedAt   time.Time       `json:"createdAt"`
}
