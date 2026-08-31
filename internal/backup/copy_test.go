package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func TestCopyRecordsCombinesCopyOrValuesFailureWithCloseValidationFailure(t *testing.T) {
	const secret = "postgres://unsafe-copy-cause"
	for _, test := range []struct {
		name          string
		valuesFailure bool
	}{
		{name: "copy failure"},
		{name: "values failure", valuesFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := openReferenceFixtureArchive(t, func(data *referenceFixtureData) {
				data.workflows[1].Description = strings.Repeat("x", 128<<10)
			})
			defer archive.Close()
			transaction := &restoreCopyFailureTx{failure: errors.New(secret), valuesFailure: test.valuesFailure}
			values := func(record WorkflowRecord) ([]any, error) {
				if test.valuesFailure {
					return nil, errors.New(secret)
				}
				return []any{record.ID}, nil
			}

			_, err := copyRecordsTo(context.Background(), transaction, archive, TableWorkflows, "unused",
				[]string{"id"}, decodeWorkflowRecord, values)
			codes := backupCodesInChain(err)
			if CodeOf(err) != CodeArchiveInvalid || codes[CodeArchiveInvalid] != 1 || codes[CodeRestoreFailed] != 1 {
				t.Fatalf("primary=%q codes=%v err=%v", CodeOf(err), codes, err)
			}
			assertErrorChainOmits(t, err, secret)
		})
	}
}

type restoreCopyFailureTx struct {
	pgx.Tx
	failure       error
	valuesFailure bool
}

func (transaction *restoreCopyFailureTx) CopyFrom(
	_ context.Context,
	_ pgx.Identifier,
	_ []string,
	source pgx.CopyFromSource,
) (int64, error) {
	if !source.Next() {
		return 0, source.Err()
	}
	if transaction.valuesFailure {
		_, err := source.Values()
		return 0, err
	}
	return 0, transaction.failure
}

func backupCodesInChain(err error) map[Code]int {
	result := make(map[Code]int)
	var visit func(error)
	visit = func(current error) {
		if current == nil {
			return
		}
		if coded, ok := current.(*Error); ok {
			result[coded.code]++
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child)
			}
			return
		}
		if wrapped, ok := current.(interface{ Unwrap() error }); ok {
			visit(wrapped.Unwrap())
		}
	}
	visit(err)
	return result
}

func assertErrorChainOmits(t *testing.T, err error, secret string) {
	t.Helper()
	var visit func(error)
	visit = func(current error) {
		if current == nil {
			return
		}
		if strings.Contains(current.Error(), secret) {
			t.Fatalf("unsafe error chain element=%T %v", current, current)
		}
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				visit(child)
			}
			return
		}
		if wrapped, ok := current.(interface{ Unwrap() error }); ok {
			visit(wrapped.Unwrap())
		}
	}
	visit(err)
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
