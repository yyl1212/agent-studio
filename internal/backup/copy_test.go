package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (reader *trackingReadCloser) Close() error {
	reader.closed = true
	return nil
}

func TestRecordSourceStreamsBoundedTypedRecordsAndCloses(t *testing.T) {
	reader := &trackingReadCloser{Reader: strings.NewReader("{\"value\":1}\n{\"value\":2}\n")}
	type record struct {
		Value int `json:"value"`
	}
	source := newRecordSource(context.Background(), reader,
		func(raw json.RawMessage) (record, error) { return decodeRecord[record](raw) },
		func(item record) ([]any, error) { return []any{item.Value}, nil },
	)

	var got []int
	for source.Next() {
		values, err := source.Values()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, values[0].(int))
	}
	if err := source.Err(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if !reader.closed || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("closed=%t values=%v", reader.closed, got)
	}
}

func TestRecordSourceRejectsOversizedUnknownAndCancelledRecords(t *testing.T) {
	type record struct {
		Value int `json:"value"`
	}
	decode := func(raw json.RawMessage) (record, error) { return decodeRecord[record](raw) }
	values := func(item record) ([]any, error) { return []any{item.Value}, nil }

	t.Run("oversized", func(t *testing.T) {
		reader := &trackingReadCloser{Reader: strings.NewReader(strings.Repeat("x", MaxRecordBytes+1) + "\n")}
		source := newRecordSource(context.Background(), reader, decode, values)
		if source.Next() || CodeOf(source.Err()) != CodeSizeLimit {
			t.Fatalf("next=%t code=%q err=%v", source.Next(), CodeOf(source.Err()), source.Err())
		}
		_ = source.Close()
	})

	t.Run("unknown field", func(t *testing.T) {
		reader := &trackingReadCloser{Reader: strings.NewReader("{\"value\":1,\"extra\":true}\n")}
		source := newRecordSource(context.Background(), reader, decode, values)
		if source.Next() || CodeOf(source.Err()) != CodeArchiveInvalid {
			t.Fatalf("code=%q err=%v", CodeOf(source.Err()), source.Err())
		}
		_ = source.Close()
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := &trackingReadCloser{Reader: strings.NewReader("{\"value\":1}\n")}
		source := newRecordSource(ctx, reader, decode, values)
		if source.Next() || !errors.Is(source.Err(), context.Canceled) {
			t.Fatalf("err=%v", source.Err())
		}
		_ = source.Close()
	})
}
