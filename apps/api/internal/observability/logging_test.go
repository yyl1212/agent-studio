package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestLogAddsOnlyPresentCorrelationFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{2},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	Log(ctx, logger, slog.LevelInfo, "run completed", IDs{
		RequestID: "request-1",
		RunID:     "run-1",
	}, slog.String("status", "completed"))

	fields := decodeLogLine(t, output.Bytes())
	if fields["requestId"] != "request-1" {
		t.Fatalf("requestId = %#v", fields["requestId"])
	}
	if fields["traceId"] != "01000000000000000000000000000000" {
		t.Fatalf("traceId = %#v", fields["traceId"])
	}
	if fields["spanId"] != "0200000000000000" {
		t.Fatalf("spanId = %#v", fields["spanId"])
	}
	if fields["run_id"] != "run-1" || fields["status"] != "completed" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if _, exists := fields["node_id"]; exists {
		t.Fatalf("empty node ID was logged: %#v", fields)
	}
}

func TestLogUsesContextRequestIDUnlessExplicitlyOverridden(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	ctx := ContextWithRequestID(context.Background(), "context-request")
	ctx = ContextWithRequestID(ctx, "")

	Log(ctx, logger, slog.LevelInfo, "from context", IDs{})
	first := decodeLogLine(t, nextJSONLine(t, &output))
	if first["requestId"] != "context-request" {
		t.Fatalf("context requestId = %#v", first["requestId"])
	}

	Log(ctx, logger, slog.LevelInfo, "explicit", IDs{RequestID: "explicit-request"})
	second := decodeLogLine(t, nextJSONLine(t, &output))
	if second["requestId"] != "explicit-request" {
		t.Fatalf("explicit requestId = %#v", second["requestId"])
	}
	if RequestIDFromContext(ctx) != "context-request" {
		t.Fatalf("RequestIDFromContext() = %q", RequestIDFromContext(ctx))
	}
}

func TestLogOmitsInvalidTraceAndEmptyIDs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	Log(context.Background(), logger, slog.LevelInfo, "empty", IDs{})

	fields := decodeLogLine(t, output.Bytes())
	for _, key := range []string{"requestId", "traceId", "spanId", "run_id", "node_id"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("empty correlation key %q was logged: %#v", key, fields)
		}
	}
}

func TestContextErrorCategoryUsesFiniteSafeValues(t *testing.T) {
	tests := []struct {
		err  error
		want ErrorCategory
	}{
		{err: nil, want: ""},
		{err: context.Canceled, want: ErrorCategoryCancelled},
		{err: errors.Join(errors.New("wrapper"), context.Canceled), want: ErrorCategoryCancelled},
		{err: context.DeadlineExceeded, want: ErrorCategoryTimeout},
		{err: errors.New("secret-error-body"), want: ErrorCategoryInternal},
	}
	for _, test := range tests {
		if got := ContextErrorCategory(test.err); got != test.want {
			t.Fatalf("ContextErrorCategory(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestSafeCategoryLoggingDoesNotExposeErrorBody(t *testing.T) {
	const sentinel = "secret-third-party-response"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	category := ContextErrorCategory(errors.New(sentinel))
	Log(context.Background(), logger, slog.LevelError, "operation failed", IDs{},
		slog.String("error_category", string(category)),
	)

	if bytes.Contains(output.Bytes(), []byte(sentinel)) {
		t.Fatalf("error body leaked: %s", output.String())
	}
	fields := decodeLogLine(t, output.Bytes())
	if fields["error_category"] != "internal" {
		t.Fatalf("error_category = %#v", fields["error_category"])
	}
}

func TestLogWithNilLoggerIsNoop(t *testing.T) {
	Log(context.Background(), nil, slog.LevelInfo, "ignored", IDs{})
}

func decodeLogLine(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode log %q: %v", encoded, err)
	}
	return fields
}

func nextJSONLine(t *testing.T, output *bytes.Buffer) []byte {
	t.Helper()
	line, err := output.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read log line: %v", err)
	}
	return line
}
