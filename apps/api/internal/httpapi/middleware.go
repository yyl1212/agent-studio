package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/yyl1212/agent-studio/apps/api/internal/observability"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (handler *handler) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				trace.SpanFromContext(request.Context()).SetStatus(codes.Error, "panic")
				observability.Log(request.Context(), handler.dependencies.Logger, slog.LevelError, "HTTP panic recovered", observability.IDs{},
					slog.String("error_category", string(observability.ErrorCategoryPanic)),
				)
				writeError(writer, request, errHandlerPanic)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func (handler *handler) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		wrapped := responseWriter(writer, request)
		completed := false
		defer func() {
			writtenStatus := wrapped.Status()
			status := normalizedHTTPStatus(writtenStatus)
			if !completed && writtenStatus == 0 {
				status = http.StatusInternalServerError
			}
			observability.Log(request.Context(), handler.dependencies.Logger, slog.LevelInfo, "HTTP request", observability.IDs{},
				slog.String("method", normalizedHTTPMethod(request.Method)),
				slog.String("route", routeTemplate(request)),
				slog.Int("status", status),
				slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			)
		}()
		next.ServeHTTP(wrapped, request)
		completed = true
	})
}

func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if origin != "" && origin == allowedOrigin {
				writer.Header().Set("Access-Control-Allow-Origin", origin)
				writer.Header().Set("Vary", "Origin")
				writer.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,OPTIONS")
				writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			}
			if request.Method == http.MethodOptions {
				writer.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(writer, request)
		})
	}
}
