package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

const errorString = "\nGot:\t%v\nWant:\t%v\n"

func TestQueueAndStack(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("core")) })

	// sign is middleware that writes msg into w. In this
	// testing context, it is used to reveal the sequence
	// of execution.
	sign := func(msg string) func(h http.Handler) http.Handler {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(msg))
				h.ServeHTTP(w, r)
			})
		}
	}
	ms := []func(h http.Handler) http.Handler{sign("m1_"), sign("m2_")}

	cases := map[string]struct {
		handler http.Handler
		want    string
	}{
		"queue": {
			handler: Queue(ms...)(handler),
			want:    "m2_m1_core",
		},
		"stack": {
			handler: Stack(ms...)(handler),
			want:    "m1_m2_core",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
			if got := w.Body.String(); got != c.want {
				t.Errorf(errorString, got, c.want)
			}
		})
	}
}

func TestCaptureWriter_WriteHeader(t *testing.T) {
	cases := map[string]struct {
		status     int
		wantStatus int
		wantPanic  bool
	}{
		"ivalid": {status: 9999, wantPanic: true},
		"bad":    {status: http.StatusBadRequest, wantStatus: http.StatusBadRequest},
		"zero":   {status: 0, wantStatus: http.StatusOK},
		"ok":     {status: http.StatusOK, wantStatus: http.StatusOK},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				r := recover()
				if c.wantPanic && r == nil {
					t.Fatalf("did not get panic for status %d", c.status)
				}
				if !c.wantPanic && r != nil {
					t.Fatalf("got panic for status %d", c.status)
				}
			}()

			w := captureWriter{ResponseWriter: httptest.NewRecorder()}
			w.WriteHeader(c.status)
			if got := w.status; got != c.wantStatus {
				t.Fatalf(errorString, got, c.wantStatus)
			}
		})
	}
}

func TestAccessLoggerOutput(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	defaultSubstrings := []string{
		"src=",
		"method=GET",
		"dest=/",
		"proto=HTTP/1.1",
		"status=200",
		"agent=",
		"duration=",
	}

	cases := map[string]struct {
		defaultLogger bool
		prefix        string
		want          string
	}{
		"logger is nil": {defaultLogger: true},
		"no prefix":     {prefix: "", want: "msg="},
		"with prefix":   {prefix: "foo", want: "msg=foo"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			var logger *slog.Logger
			if !c.defaultLogger {
				logger = slog.New(slog.NewTextHandler(&buf, nil))
			}

			w := httptest.NewRecorder()
			h := AccessLogger(logger, c.prefix)(handler)
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

			got := buf.String()
			if !c.defaultLogger {
				for i, sub := range defaultSubstrings {
					if !strings.Contains(got, sub) {
						t.Errorf("did not get wanted substring %s", defaultSubstrings[i])
					}
				}
				if !strings.Contains(got, c.want) {
					t.Errorf(errorString, got, c.want)
				}
			} else {
				for i, sub := range defaultSubstrings {
					if strings.Contains(got, sub) {
						t.Errorf("Got unwanted substring %s, when buf should be empty", defaultSubstrings[i])
					}
				}
			}
		})
	}
}

func TestRecoverAndHandle(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := r.URL.Query().Get("id")
		if _, err := strconv.Atoi(sid); err != nil {
			panic("reached panic")
		}
		w.Write([]byte("ok"))
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("caught panic"))
	})
	cases := map[string]struct {
		url        string
		logger     *slog.Logger
		wantBody   string
		wantStatus int
	}{
		"panic, fallback, logger is nil": {
			url:        "/?id=foo",
			logger:     nil,
			wantStatus: http.StatusInternalServerError,
			wantBody:   "caught panic",
		},
		"panic, fallback, logger is non-nil": {
			url:        "/?id=foo",
			logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "caught panic",
		},
		"no panic": {
			url:        "/?id=10",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h := RecoverAndHandle(fallback, c.logger)(handler)
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.url, nil))
			if got := w.Code; got != c.wantStatus {
				t.Fatalf(errorString, got, c.wantStatus)
			}
			if got := w.Body.String(); got != c.wantBody {
				t.Fatalf(errorString, got, c.wantBody)
			}
		})
	}
}
