package messages

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const errorString = "\nGot:\t%v\nWant:\t%v\n"

var textLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))

func TestResponder_Logger(t *testing.T) {
	cases := map[string]struct {
		logger *slog.Logger
		want   *slog.Logger
	}{
		"logger is nil":     {nil, slog.Default()},
		"logger is non-nil": {textLogger, textLogger},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := (Responder{Logger: c.logger}.logger()); got != c.want {
				t.Errorf(errorString, got, c.want)
			}
		})
	}
}

var (
	simpleResponseHandlerFunc = func(r *http.Request) Response {
		return Response{
			Status:    http.StatusOK,
			Headers:   http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			WriteBody: func(w io.Writer) error { _, err := io.WriteString(w, "hello, world"); return err },
		}
	}

	erroredResponseHandlerFunc = func(r *http.Request) Response {
		return Response{
			Status:    http.StatusOK,
			Headers:   http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			WriteBody: func(w io.Writer) error { return errors.New("fail on purpose") },
		}
	}
)

// errorResponseWriter wraps a ResponseWriter but overrides
// Write to fail. It's used to test buffer the flushing
// error branch.
type errorResponseWriter struct {
	http.ResponseWriter
}

func (rw *errorResponseWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write failure")
}

func TestResponder_HandlerFunc(t *testing.T) {
	cases := map[string]struct {
		re              Responder
		handler         ResponseHandlerFunc
		forceWriteError bool
		wantCode        int
		wantHeader      http.Header
		wantBody        string
		wantLog         string
	}{
		"err - invalid status": {
			re:       Responder{},
			handler:  func(r *http.Request) Response { return Response{Status: 0} },
			wantCode: http.StatusInternalServerError,
			wantLog:  "invalid status",
		},
		"err - stream write error": {
			re:      Responder{},
			handler: erroredResponseHandlerFunc,
			// http.Error is a no-op after committing status
			// so it is not used and code below is not changed
			wantCode: http.StatusOK,
			wantLog:  "Error streaming",
		},
		"err - buffer write error": {
			re:       Responder{Buffer: true},
			handler:  erroredResponseHandlerFunc,
			wantCode: http.StatusInternalServerError,
			wantLog:  "Error buffering",
		},
		"err - buffer flush error": {
			re:      Responder{Buffer: true},
			handler: simpleResponseHandlerFunc,
			// flag to inject a writer that will force the
			// buffer flush error
			forceWriteError: true,
			// http.Error is a no-op after committing status
			// so it is not used and code below is not changed
			wantCode: http.StatusOK,
			wantBody: "",
			wantLog:  "Error flushing",
		},
		"ok - body nil": {
			re: Responder{},
			handler: func(r *http.Request) Response {
				return Response{Status: http.StatusNoContent, Headers: http.Header{"X-Custom": {"no body"}}}
			},
			wantCode:   http.StatusNoContent,
			wantHeader: http.Header{"X-Custom": {"no body"}},
		},
		"ok - body streamed": {
			re:         Responder{},
			handler:    simpleResponseHandlerFunc,
			wantCode:   http.StatusOK,
			wantHeader: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			wantBody:   "hello, world",
		},
		"ok - body buffered": {
			re:         Responder{Buffer: true},
			handler:    simpleResponseHandlerFunc,
			wantCode:   http.StatusOK,
			wantHeader: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
			wantBody:   "hello, world",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			c.re.Logger = slog.New(slog.NewTextHandler(&buf, nil))

			h := c.re.HandlerFunc(c.handler)
			rec := httptest.NewRecorder()
			w := http.ResponseWriter(rec)
			if c.forceWriteError == true {
				w = &errorResponseWriter{w}
			}

			h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

			if rec.Code != c.wantCode {
				t.Errorf(errorString, rec.Code, c.wantCode)
			}
			if c.wantBody != "" && rec.Body.String() != c.wantBody {
				t.Errorf(errorString, rec.Body.String(), c.wantBody)
			}
			if c.wantHeader != nil {
				for key, vals := range c.wantHeader {
					got := rec.Header().Values(key)
					if len(got) == 0 || got[0] != vals[0] {
						t.Errorf(errorString, got, vals)
					}
				}
			}
			if c.wantLog != "" && !strings.Contains(buf.String(), c.wantLog) {
				t.Errorf(errorString, buf.String(), c.wantLog)
			}
		})
	}
}
