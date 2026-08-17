package domain

import "errors"

var (
	ErrNotFound         = errors.New("record not found")
	ErrRevisionConflict = errors.New("workflow revision conflict")
	ErrSlugConflict     = errors.New("workflow slug conflict")
)

type PublicError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
}

type ValidationIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
	EdgeID  string `json:"edgeId,omitempty"`
	Path    string `json:"path,omitempty"`
}
