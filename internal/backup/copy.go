package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
)

type recordSource[T any] struct {
	ctx      context.Context
	reader   io.ReadCloser
	buffered *bufio.Reader
	current  T
	err      error
	decode   func(json.RawMessage) (T, error)
	values   func(T) ([]any, error)
	finished bool
}

func newRecordSource[T any](
	ctx context.Context,
	reader io.ReadCloser,
	decode func(json.RawMessage) (T, error),
	values func(T) ([]any, error),
) *recordSource[T] {
	return &recordSource[T]{
		ctx: ctx, reader: reader, buffered: bufio.NewReaderSize(reader, 64<<10), decode: decode, values: values,
	}
}

func (source *recordSource[T]) Next() bool {
	if source.finished || source.err != nil {
		return false
	}
	if err := source.ctx.Err(); err != nil {
		source.err = err
		return false
	}
	line, eof, err := readBoundedLine(source.buffered)
	if err != nil {
		source.err = err
		return false
	}
	if eof && len(line) == 0 {
		source.finished = true
		return false
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		source.err = Wrap(CodeArchiveInvalid, "validate final backup record newline", nil)
		return false
	}
	if err := validateJSONObject(line[:len(line)-1]); err != nil {
		source.err = err
		return false
	}
	current, err := source.decode(json.RawMessage(line[:len(line)-1]))
	if err != nil {
		source.err = err
		return false
	}
	source.current = current
	return true
}

func (source *recordSource[T]) Values() ([]any, error) {
	if source.err != nil {
		return nil, source.err
	}
	return source.values(source.current)
}

func (source *recordSource[T]) Err() error { return source.err }

func (source *recordSource[T]) Close() error { return source.reader.Close() }
