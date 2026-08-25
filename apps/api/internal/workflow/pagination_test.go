package workflow

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

const testCursorID = "11111111-1111-4111-8111-111111111111"

func TestPageCursorRoundTripAndFilterBinding(t *testing.T) {
	timestamp := time.Date(2026, 8, 25, 12, 0, 0, 123, time.UTC)
	filter := filterFingerprint(struct {
		Query string `json:"q"`
		State string `json:"state"`
	}{Query: "agent", State: "active"})

	raw, err := encodePageCursor(timestamp, testCursorID, filter)
	if err != nil {
		t.Fatal(err)
	}
	decodedTime, decodedID, err := decodePageCursor(raw, filter)
	if err != nil {
		t.Fatal(err)
	}
	if !decodedTime.Equal(timestamp) || decodedID != testCursorID {
		t.Fatalf("time=%s id=%q", decodedTime, decodedID)
	}
	if _, _, err := decodePageCursor(raw, filterFingerprint("archived")); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("mismatched filter error=%v", err)
	}
}

func TestPageCursorRejectsMalformedWireValues(t *testing.T) {
	filter := strings.Repeat("a", 64)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "not base64url", raw: "%%%"},
		{name: "unknown field", raw: testEncodedCursor(`{"v":1,"time":"2026-08-25T12:00:00Z","id":"11111111-1111-4111-8111-111111111111","filter":"` + filter + `","extra":true}`)},
		{name: "unsupported version", raw: testEncodedCursor(`{"v":2,"time":"2026-08-25T12:00:00Z","id":"11111111-1111-4111-8111-111111111111","filter":"` + filter + `"}`)},
		{name: "invalid uuid", raw: testEncodedCursor(`{"v":1,"time":"2026-08-25T12:00:00Z","id":"not-a-uuid","filter":"` + filter + `"}`)},
		{name: "non utc time", raw: testEncodedCursor(`{"v":1,"time":"2026-08-25T20:00:00+08:00","id":"11111111-1111-4111-8111-111111111111","filter":"` + filter + `"}`)},
		{name: "empty filter", raw: testEncodedCursor(`{"v":1,"time":"2026-08-25T12:00:00Z","id":"11111111-1111-4111-8111-111111111111","filter":""}`)},
		{name: "trailing json", raw: testEncodedCursor(`{"v":1,"time":"2026-08-25T12:00:00Z","id":"11111111-1111-4111-8111-111111111111","filter":"` + filter + `"}{}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := decodePageCursor(test.raw, filter); !errors.Is(err, ErrCursorInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPageCursorRejectsValidWireBeyond512Bytes(t *testing.T) {
	filter := strings.Repeat("b", 400)
	raw := testEncodedCursor(`{"v":1,"time":"2026-08-25T12:00:00Z","id":"11111111-1111-4111-8111-111111111111","filter":"` + filter + `"}`)
	if len(raw) <= 512 {
		t.Fatalf("test cursor bytes=%d, want over 512", len(raw))
	}
	if _, _, err := decodePageCursor(raw, filter); !errors.Is(err, ErrCursorInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestPageCursorEncoderRejectsInvalidInputs(t *testing.T) {
	validTime := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		at     time.Time
		id     string
		filter string
	}{
		{name: "non utc", at: time.Date(2026, 8, 25, 20, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60)), id: testCursorID, filter: "filter"},
		{name: "invalid uuid", at: validTime, id: "not-a-uuid", filter: "filter"},
		{name: "empty filter", at: validTime, id: testCursorID, filter: ""},
		{name: "encoded budget", at: validTime, id: testCursorID, filter: strings.Repeat("b", 400)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := encodePageCursor(test.at, test.id, test.filter); !errors.Is(err, ErrCursorInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func testEncodedCursor(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
