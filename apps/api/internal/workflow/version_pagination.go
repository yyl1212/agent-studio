package workflow

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

const versionCursorWireVersion = 1

type versionCursor struct {
	WireVersion   int    `json:"v"`
	WorkflowID    string `json:"workflowId"`
	BeforeVersion int    `json:"beforeVersion"`
}

func encodeVersionCursor(workflowID string, beforeVersion int) (string, error) {
	parsedWorkflowID, err := uuid.Parse(workflowID)
	if err != nil || beforeVersion <= 0 {
		return "", ErrCursorInvalid
	}
	encoded, err := json.Marshal(versionCursor{
		WireVersion:   versionCursorWireVersion,
		WorkflowID:    parsedWorkflowID.String(),
		BeforeVersion: beforeVersion,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode version cursor: %v", ErrCursorInvalid, err)
	}
	raw := base64.RawURLEncoding.EncodeToString(encoded)
	if len(raw) > maxManagementCursorBytes {
		return "", ErrCursorInvalid
	}
	return raw, nil
}

func decodeVersionCursor(raw, expectedWorkflowID string) (int, error) {
	if raw == "" || len(raw) > maxManagementCursorBytes {
		return 0, ErrCursorInvalid
	}
	expected, err := uuid.Parse(expectedWorkflowID)
	if err != nil {
		return 0, ErrCursorInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > maxManagementCursorBytes {
		return 0, ErrCursorInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor versionCursor
	if err := decoder.Decode(&cursor); err != nil {
		return 0, fmt.Errorf("%w: decode version cursor: %v", ErrCursorInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return 0, ErrCursorInvalid
	}
	cursorWorkflowID, err := uuid.Parse(cursor.WorkflowID)
	if err != nil || cursor.WireVersion != versionCursorWireVersion || cursor.BeforeVersion <= 0 || cursorWorkflowID != expected {
		return 0, ErrCursorInvalid
	}
	return cursor.BeforeVersion, nil
}
