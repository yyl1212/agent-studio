package httpapi

import (
	"net/http"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func (handler *handler) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				handler.dependencies.Logger.Error("HTTP panic recovered", "requestId", chimiddleware.GetReqID(request.Context()))
				writeError(writer, request, errHandlerPanic)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func (handler *handler) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		wrapped := chimiddleware.NewWrapResponseWriter(writer, request.ProtoMajor)
		next.ServeHTTP(wrapped, request)
		handler.dependencies.Logger.Info("HTTP request",
			"requestId", chimiddleware.GetReqID(request.Context()),
			"method", request.Method,
			"path", request.URL.Path,
			"status", wrapped.Status(),
			"durationMs", time.Since(started).Milliseconds(),
		)
	})
}

func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if origin != "" && origin == allowedOrigin {
				writer.Header().Set("Access-Control-Allow-Origin", origin)
				writer.Header().Set("Vary", "Origin")
				writer.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,OPTIONS")
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
