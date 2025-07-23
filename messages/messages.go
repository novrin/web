// Package messages provides a declarative and composable
// structure for defining HTTP responses in Go.
//
// It introduces the Response type, which encapsulates
// HTTP status, headers, and a response body writer.
// This allows handler functions to focus on returning data
// rather than writing directly to http.ResponseWriter.
//
// The package also includes the Responder type, which
// adapts declarative handler functions (ResponseHandlerFunc)
// into standard http.HandlerFunc values, with built-in
// error handling, centralized logging, and optional
// buffered output to ensure atomic HTTP responses.
package messages

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
)

// Response defines a structured HTTP response message.
//
// It includes a status code, headers, and an optional
// WriteBody function, which writes the response body to an
// io.Writer. If WriteBody is nil, only the headers and
// status are sent.
//
// This type is returned from a ResponseHandlerFunc and
// processed by a Responder.
type Response struct {
	Status    int
	Headers   http.Header
	WriteBody func(io.Writer) error
}

// ResponseHandlerFunc defines a declarative HTTP handler function.
//
// It accepts an *http.Request and returns a structured Response.
// Use Responder.HandlerFunc to adapt this function into
// a standard http.HandlerFunc.
type ResponseHandlerFunc func(*http.Request) Response

// Responder provides a centralized mechanism for sending
// Response values. It encapsulates configuration for
// response buffering and structured error logging.
//
// Use Responder.HandlerFunc to convert a ResponseHandlerFunc
// into an http.HandlerFunc.
type Responder struct {
	// Logger is used to log internal errors encountered
	// while writing responses. If nil, errors will logged
	// using slog.Default().
	Logger *slog.Logger

	// Buffer indicates whether a response body should be
	// buffered internally before sending it to the client.
	// If true, responses are only sent if writing succeeds,
	// ensuring atomicity. If false, the response body
	// streams directly.
	Buffer bool
}

// logger returns the configured Logger, or defaults to slog.Default().
func (re Responder) logger() *slog.Logger {
	if re.Logger == nil {
		return slog.Default()
	}
	return re.Logger
}

// HandlerFunc wraps a ResponseHandlerFunc using re's
// configuration, returning a standard http.HandlerFunc.
//
// It handles writing status, headers, and body. If
// buffering is enabled, the body is written to an
// internal buffer and flushed only if writing succeeds,
// preventing partial writes.
//
// If the handler returns a Response with an invalid Status,
// a 500 Internal Server Error is sent and logged. Errors
// during body writing or buffering are also logged but
// HTTP errors are only sent where possible - i.e. where
// Status has not been committed.
func (re Responder) HandlerFunc(fn ResponseHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := fn(r)

		if resp.Status < 100 || resp.Status > 599 {
			re.logger().Error("Handler returned a Response with invalid status code",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		for k, vals := range resp.Headers {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}

		if resp.WriteBody == nil {
			w.WriteHeader(resp.Status)
			return
		}

		if !re.Buffer {
			w.WriteHeader(resp.Status)
			if err := resp.WriteBody(w); err != nil {
				re.logger().Error("Error streaming response body",
					slog.String("path", r.URL.Path),
					slog.String("error", err.Error()),
				)
				// Status committed - too late to send HTTP error response.
			}
			return
		}

		var buf bytes.Buffer
		if err := resp.WriteBody(&buf); err != nil {
			re.logger().Error("Error buffering response body",
				slog.String("path", r.URL.Path),
				slog.String("error", err.Error()),
			)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(resp.Status)
		if _, err := buf.WriteTo(w); err != nil {
			re.logger().Error("Error flushing buffered response",
				slog.String("path", r.URL.Path),
				slog.String("error", err.Error()),
			)
			// Status committed - too late to send HTTP error response.
		}
	}
}
