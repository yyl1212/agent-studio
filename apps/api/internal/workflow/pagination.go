package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

var ErrCursorInvalid = errors.New("management cursor invalid")

const managementCursorVersion = 1
const maxManagementCursorBytes = 512

type pageCursor struct {
	Version int       `json:"v"`
	Time    time.Time `json:"time"`
	ID      string    `json:"id"`
	Filter  string    `json:"filter"`
}

func filterFingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode cursor filter: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func encodePageCursor(at time.Time, id, filter string) (string, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil || at.Location() != time.UTC || filter == "" {
		return "", ErrCursorInvalid
	}
	encoded, err := json.Marshal(pageCursor{
		Version: managementCursorVersion,
		Time:    at,
		ID:      parsedID.String(),
		Filter:  filter,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode: %v", ErrCursorInvalid, err)
	}
	raw := base64.RawURLEncoding.EncodeToString(encoded)
	if len(raw) > maxManagementCursorBytes {
		return "", ErrCursorInvalid
	}
	return raw, nil
}

func decodePageCursor(raw, expectedFilter string) (time.Time, string, error) {
	if raw == "" || len(raw) > maxManagementCursorBytes || expectedFilter == "" {
		return time.Time{}, "", ErrCursorInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > maxManagementCursorBytes {
		return time.Time{}, "", ErrCursorInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor pageCursor
	if err := decoder.Decode(&cursor); err != nil {
		return time.Time{}, "", fmt.Errorf("%w: decode: %v", ErrCursorInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return time.Time{}, "", ErrCursorInvalid
	}
	parsedID, err := uuid.Parse(cursor.ID)
	if err != nil || cursor.Version != managementCursorVersion || cursor.Time.Location() != time.UTC || cursor.Filter == "" || cursor.Filter != expectedFilter {
		return time.Time{}, "", ErrCursorInvalid
	}
	return cursor.Time, parsedID.String(), nil
}
