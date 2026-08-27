package observability

import (
	"context"
	"errors"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type ErrorCategory string

const (
	ErrorCategoryValidation    ErrorCategory = "validation"
	ErrorCategoryConflict      ErrorCategory = "conflict"
	ErrorCategoryCapacity      ErrorCategory = "capacity"
	ErrorCategoryCancelled     ErrorCategory = "cancelled"
	ErrorCategoryTimeout       ErrorCategory = "timeout"
	ErrorCategoryNodeExecution ErrorCategory = "node_execution"
	ErrorCategoryPersistence   ErrorCategory = "persistence"
	ErrorCategoryPanic         ErrorCategory = "panic"
	ErrorCategoryInternal      ErrorCategory = "internal"
)

type IDs struct {
	RequestID string
	RunID     string
	NodeID    string
}

type requestIDContextKey struct{}

func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func Log(ctx context.Context, logger *slog.Logger, level slog.Level, message string, ids IDs, attrs ...slog.Attr) {
	if logger == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fields := make([]slog.Attr, 0, len(attrs)+5)
	if ids.RequestID == "" {
		ids.RequestID = RequestIDFromContext(ctx)
	}
	if ids.RequestID != "" {
		fields = append(fields, slog.String("requestId", ids.RequestID))
	}
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		fields = append(fields,
			slog.String("traceId", spanContext.TraceID().String()),
			slog.String("spanId", spanContext.SpanID().String()),
		)
	}
	if ids.RunID != "" {
		fields = append(fields, slog.String("run_id", ids.RunID))
	}
	if ids.NodeID != "" {
		fields = append(fields, slog.String("node_id", ids.NodeID))
	}
	fields = append(fields, attrs...)
	logger.LogAttrs(ctx, level, message, fields...)
}

func ContextErrorCategory(err error) ErrorCategory {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return ErrorCategoryCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorCategoryTimeout
	}
	return ErrorCategoryInternal
}
