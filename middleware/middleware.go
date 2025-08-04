// Package middleware provides composable HTTP middleware
// for use with net/http. It includes utilities for panic
// recovery, access logging, and middleware chaining.
package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// captureWriter wraps http.ResponseWriter and captures
// the HTTP status code written to the client. It is used
// internally by AccessLogger.
type captureWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code and delegates to
// the underlying ResponseWriter. If statusCode is zero,
// http.StatusOK is assumed.
func (rw *captureWriter) WriteHeader(statusCode int) {
	if statusCode == 0 { // must mimic Go internals
		statusCode = http.StatusOK
	}
	rw.status = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// AccessLogger returns middleware that logs request
// and response details. If logger is nil, slog.Default()
// is used.
//
// Fields logged include remote address, method, URL,
// protocol, response status, user agent, and request
// duration.
func AccessLogger(logger *slog.Logger, prefix string) func(h http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &captureWriter{ResponseWriter: w}
			h.ServeHTTP(rw, r)
			logger.Info(
				prefix,
				slog.String("src", r.RemoteAddr),
				slog.String("method", r.Method),
				slog.String("dest", r.URL.RequestURI()),
				slog.String("proto", r.Proto),
				slog.Int("status", rw.status),
				slog.String("user-agent", r.Header.Get("User-Agent")),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// RecoverAndHandle returns middleware that recovers from
// panics in downstream handlers. If a panic occurs, it
// logs the error and stack trace using logger, then
// delegates to the fallback handler.
//
// If logger is nil, slog.Default() is used.
func RecoverAndHandle(logger *slog.Logger, fallback http.Handler) func(h http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("PANIC caught by middleware.RecoverAndHandle",
						slog.String("error", fmt.Sprintf("%v", rec)),
						slog.Any("stack", strings.Split(string(debug.Stack()), "\n")),
					)
					fallback.ServeHTTP(w, r)
				}
			}()
			h.ServeHTTP(w, r)
		})
	}
}
