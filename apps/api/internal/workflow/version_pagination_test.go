package workflow

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestVersionCursorRoundTripAndWorkflowBinding(t *testing.T) {
	raw, err := encodeVersionCursor("11111111-1111-4111-8111-111111111111", 7)
	if err != nil {
		t.Fatal(err)
	}
	before, err := decodeVersionCursor(raw, "11111111-1111-4111-8111-111111111111")
	if err != nil || before != 7 {
		t.Fatalf("before=%d err=%v", before, err)
	}
	if _, err := decodeVersionCursor(raw, "22222222-2222-4222-8222-222222222222"); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("cross-workflow cursor err=%v", err)
	}
}

func TestVersionCursorRejectsMalformedWireValues(t *testing.T) {
	workflowID := "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "not base64url", raw: "%%%"},
		{name: "unknown field", raw: encodedVersionCursor(`{"v":1,"workflowId":"11111111-1111-4111-8111-111111111111","beforeVersion":7,"extra":true}`)},
		{name: "unsupported wire version", raw: encodedVersionCursor(`{"v":2,"workflowId":"11111111-1111-4111-8111-111111111111","beforeVersion":7}`)},
		{name: "invalid workflow", raw: encodedVersionCursor(`{"v":1,"workflowId":"invalid","beforeVersion":7}`)},
		{name: "nonpositive version", raw: encodedVersionCursor(`{"v":1,"workflowId":"11111111-1111-4111-8111-111111111111","beforeVersion":0}`)},
		{name: "trailing json", raw: encodedVersionCursor(`{"v":1,"workflowId":"11111111-1111-4111-8111-111111111111","beforeVersion":7}{}`)},
		{name: "over budget", raw: strings.Repeat("a", 513)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeVersionCursor(test.raw, workflowID); !errors.Is(err, ErrCursorInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestVersionCursorEncoderRejectsInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		name       string
		workflowID string
		before     int
	}{
		{name: "invalid workflow", workflowID: "invalid", before: 1},
		{name: "zero version", workflowID: "11111111-1111-4111-8111-111111111111", before: 0},
		{name: "negative version", workflowID: "11111111-1111-4111-8111-111111111111", before: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := encodeVersionCursor(test.workflowID, test.before); !errors.Is(err, ErrCursorInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func encodedVersionCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
