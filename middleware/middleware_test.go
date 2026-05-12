package middleware

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCaptureWriter_WriteHeader(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantStatus int
		wantPanic  bool
	}{
		{name: "must_panic_on_invalid_status", status: 9999, wantPanic: true},
		{name: "must_pass_on_status_code_0", status: 0, wantStatus: http.StatusOK},
		{name: "must_pass_on_sample_status_ok", status: http.StatusOK, wantStatus: http.StatusOK},
		{name: "must_pass_on_sample_status_error", status: http.StatusBadRequest, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if got := (r != nil); got != tt.wantPanic {
					t.Errorf("got panic %v, want %v", got, tt.wantPanic)
				}
			}()

			w := captureWriter{ResponseWriter: httptest.NewRecorder()}
			w.WriteHeader(tt.status)
			if got := w.status; got != tt.wantStatus {
				t.Errorf("got status %v, want %v", got, tt.wantStatus)
			}
		})
	}
}

func TestAccessLogger(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	substrings := []string{
		"src=",
		"method=",
		"dest=",
		"proto=",
		"status=",
		"agent=",
		"ttr=",
	}

	o := slog.Default()

	// replace default logger to track on nil logger
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	// restore original default logger after test
	t.Cleanup(func() { slog.SetDefault(o) })

	tests := []struct {
		name   string
		logger *slog.Logger
		prefix string
		want   string
	}{
		{name: "must_pass_on_logger_nil", logger: nil, prefix: "", want: "msg="},
		{name: "must_pass_on_no_prefix", logger: logger, prefix: "", want: "msg="},
		{name: "must_pass_on_prefix", logger: logger, prefix: "foo", want: "msg=foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h := AccessLogger(tt.logger, tt.prefix)(handler)
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

			got := buf.String()
			defer buf.Reset()
			for i, sub := range substrings {
				if !strings.Contains(got, sub) {
					t.Errorf("did not get wanted substring '%s'", substrings[i])
				}
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("'%s' does not contain '%s'", got, tt.want)
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

	o := slog.Default()

	// replace default logger to track on nil logger
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	// restore original default logger after test
	t.Cleanup(func() { slog.SetDefault(o) })

	tests := []struct {
		name       string
		url        string
		logger     *slog.Logger
		wantStatus int
		wantBody   string
		wantLog    string
	}{
		{
			name:       "must_pass_on_panic_and_logger_nil",
			url:        "/?id=foo",
			logger:     nil,
			wantStatus: http.StatusInternalServerError,
			wantBody:   "caught panic",
			wantLog:    "PANIC caught by middleware.RecoverAndHandle",
		},
		{
			name:       "must_pass_on_panic_and_supplied_logger",
			url:        "/?id=foo",
			logger:     logger,
			wantStatus: http.StatusInternalServerError,
			wantBody:   "caught panic",
			wantLog:    "PANIC caught by middleware.RecoverAndHandle",
		},
		{
			name:       "must_pass_on_no_panic",
			logger:     nil,
			url:        "/?id=10",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h := RecoverAndHandle(tt.logger, fallback)(handler)

			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.url, nil))
			if got := w.Code; got != tt.wantStatus {
				t.Errorf("got status %v, want %v", got, tt.wantStatus)
			}
			if got := w.Body.String(); got != tt.wantBody {
				t.Errorf("got body %v, want %v", got, tt.wantBody)
			}
			got := buf.String()
			defer buf.Reset()
			if !strings.Contains(got, tt.wantLog) {
				t.Errorf("'%s' does not contain '%s'", got, tt.wantLog)
			}
		})
	}
}

func TestRequestLogger(t *testing.T) {
	tripper := RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Request: r}, nil
	})
	substrings := []string{
		"src=",
		"method=",
		"dest=",
		"proto=",
		"status=",
		"agent=",
		"ttr=",
	}

	o := slog.Default()

	// replace default logger to track on nil logger
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	// restore original default logger after test
	t.Cleanup(func() { slog.SetDefault(o) })

	tests := []struct {
		name   string
		logger *slog.Logger
		prefix string
		want   string
	}{
		{name: "must_pass_on_logger_nil", logger: nil, prefix: "", want: "msg="},
		{name: "must_pass_on_no_prefix", logger: logger, prefix: "", want: "msg="},
		{name: "must_pass_on_prefix", logger: logger, prefix: "foo", want: "msg=foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tripper := RequestLogger(tt.logger, tt.prefix)(tripper)
			resp, err := tripper.RoundTrip(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatalf("RoundTripper failed %s", err.Error())
			}
			if resp == nil {
				t.Fatalf("want response, got nil")
			}

			got := buf.String()
			defer buf.Reset()
			for i, sub := range substrings {
				if !strings.Contains(got, sub) {
					t.Errorf("did not get wanted substring '%s'", substrings[i])
				}
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("'%s' does not contain '%s'", got, tt.want)
			}
		})
	}
}

func TestNewCircuitBreaker(t *testing.T) {
	var threshold uint = 5
	cooldown := time.Minute
	breaker := NewCircuitBreaker(threshold, cooldown)

	if breaker.state != BreakerStateClosed {
		t.Errorf("got state %v, want %v", breaker.state, BreakerStateClosed)
	}
	if breaker.failures != 0 {
		t.Errorf("got failures %v, want 0", breaker.failures)
	}
	if breaker.threshold != threshold {
		t.Errorf("got threshold %v, want %v", breaker.threshold, threshold)
	}
	if breaker.cooldown != cooldown {
		t.Errorf("got cooldown %v, want %v", breaker.cooldown, cooldown)
	}
	past := time.Now().UTC().Add(-10 * time.Second)
	if breaker.showtime.IsZero() || breaker.showtime.Before(past) {
		t.Errorf("got showtime  %v, want time of initialization", breaker.showtime)
	}
}

func TestCircuitBreaker_State(t *testing.T) {
	tests := []struct {
		name     string
		state    BreakerState
		showtime time.Time
		want     BreakerState
	}{
		{
			name:  "must_be_true_on_closed",
			state: BreakerStateClosed,
			want:  BreakerStateClosed,
		},
		{
			name:     "must_be_false_on_open_not_past_showtime",
			state:    BreakerStateOpen,
			showtime: time.Now().UTC().Add(10 * time.Second),
			want:     BreakerStateOpen,
		},
		{
			name:     "must_flip_true_on_open_past_showtime",
			state:    BreakerStateOpen,
			showtime: time.Now().UTC().Add(-10 * time.Second),
			want:     BreakerStateClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaker := &CircuitBreaker{state: tt.state, showtime: tt.showtime}

			if got := breaker.State(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCircuitBreaker_OK(t *testing.T) {
	tests := []struct {
		name     string
		state    BreakerState
		showtime time.Time
		want     bool
	}{
		{
			name:  "must_be_true_on_closed",
			state: BreakerStateClosed,
			want:  true,
		},
		{
			name:     "must_be_false_on_open_not_past_showtime",
			state:    BreakerStateOpen,
			showtime: time.Now().UTC().Add(10 * time.Second),
			want:     false,
		},
		{
			name:     "must_flip_true_on_open_past_showtime",
			state:    BreakerStateOpen,
			showtime: time.Now().UTC().Add(-10 * time.Second),
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaker := &CircuitBreaker{state: tt.state, showtime: tt.showtime}

			if got := breaker.OK(); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCircuitBreaker_OnSuccess(t *testing.T) {
	tests := []struct {
		name     string
		failures uint
	}{
		{name: "must_reset_on_nonzero", failures: 5},
		{name: "must_remain_zero_on_zero", failures: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaker := &CircuitBreaker{failures: tt.failures}
			breaker.OnSuccess()

			if breaker.failures != 0 {
				t.Errorf("got %v, want 0", breaker.failures)
			}
		})
	}
}

func TestCircuitBreaker_OnFailure(t *testing.T) {
	const threshold uint = 5
	tests := []struct {
		name         string
		failures     uint
		wantFailures uint
		wantState    BreakerState
	}{
		{
			name:         "must_increment_failures_below_threshold",
			failures:     4,
			wantFailures: 5,
			wantState:    BreakerStateClosed,
		},
		{
			name:         "must_reset_failures_after_threshold_and_break_open",
			failures:     5,
			wantFailures: 0,
			wantState:    BreakerStateOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaker := &CircuitBreaker{failures: tt.failures, threshold: threshold}
			breaker.OnFailure()

			if got := breaker.failures; got != tt.wantFailures {
				t.Errorf("got failures %v, want %v", got, tt.wantFailures)
			}
			if got := breaker.state; got != tt.wantState {
				t.Errorf("got state %v, want %v", got, tt.wantState)
			}
			if breaker.state == BreakerStateOpen && breaker.showtime.IsZero() {
				t.Errorf("showtime was not set")
			}
		})
	}
}

func TestIsIdempotent(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		hasIdempotenceKey bool
		want              bool
	}{
		{
			name:   "must_be_false_on_method_patch_without_idempotence_key",
			method: http.MethodPatch,
			want:   false,
		},
		{
			name:   "must_be_false_on_method_post_without_idempotence_key",
			method: http.MethodPost,
			want:   false,
		},
		{
			name:              "must_be_true_on_method_patch_with_idempotence_key",
			method:            http.MethodPatch,
			hasIdempotenceKey: true,
			want:              true,
		},
		{
			name:              "must_be_true_on_method_post_with_idempotence_key",
			method:            http.MethodPost,
			hasIdempotenceKey: true,
			want:              true,
		},
		{
			name:   "must_be_true_on_method_get",
			method: http.MethodGet,
			want:   true,
		},
		{
			name:   "must_be_true_on_method_head",
			method: http.MethodHead,
			want:   true,
		},
		{
			name:   "must_be_true_on_method_options",
			method: http.MethodOptions,
			want:   true,
		},
		{
			name:   "must_be_true_on_method_trace",
			method: http.MethodTrace,
			want:   true,
		},
		{
			name:   "must_be_true_on_method_put",
			method: http.MethodPut,
			want:   true,
		},
		{
			name:   "must_be_true_on_method_delete",
			method: http.MethodDelete,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse("/")
			if err != nil {
				t.Fatalf("failed to parse example url")
			}
			r := &http.Request{
				Method: tt.method,
				URL:    u,
				Header: http.Header{},
			}
			if tt.hasIdempotenceKey {
				r.Header.Set("Idempotency-Key", crand.Text())
			}

			if got := isIdempotent(r); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryAfterValue(t *testing.T) {
	tests := []struct {
		name          string
		hasRetryAfter bool
		value         string
		wantDuration  time.Duration
		wantBool      bool
	}{
		{
			name:          "must_be_false_on_no_retry_after_header_field",
			hasRetryAfter: false,
			wantDuration:  0,
			wantBool:      false,
		},
		{
			name:          "must_be_false_on_retry_after_value_not_parsable",
			hasRetryAfter: true,
			value:         crand.Text(),
			wantDuration:  0,
			wantBool:      false,
		},
		{
			name:          "must_be_true_on_retry_after_value_int",
			hasRetryAfter: true,
			value:         "120",
			wantDuration:  120 * time.Second,
			wantBool:      true,
		},
		{
			name:          "must_be_true_on_retry_after_value_date",
			hasRetryAfter: true,
			value:         time.Now().UTC().Add(120 * time.Second).Format(http.TimeFormat),
			wantDuration:  119 * time.Second, // observe time is lowered to account for processing
			wantBool:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.hasRetryAfter {
				h.Set("Retry-After", tt.value)
			}

			got, ok := retryAfterValue(h)
			if ok != tt.wantBool {
				t.Errorf("got bool %v, want %v", ok, tt.wantBool)
			}
			if got < tt.wantDuration {
				t.Errorf("got duration %v, want > %v", got, tt.wantDuration)
			}
		})
	}
}

func TestDelay(t *testing.T) {
	tests := []struct {
		name      string
		header    http.Header
		base      time.Duration
		max       time.Duration
		count     uint
		wantPanic bool
		wantMin   time.Duration
		wantMax   time.Duration
	}{
		{
			name:      "must_panic_on_base_eq_zero",
			header:    http.Header{},
			base:      0,
			max:       5 * time.Second,
			count:     1,
			wantPanic: true,
		},
		{
			name:      "must_panic_on_max_eq_zero",
			header:    http.Header{},
			base:      1 * time.Second,
			max:       0,
			count:     1,
			wantPanic: true,
		},
		{
			name:      "must_panic_on_count_eq_zero",
			header:    http.Header{},
			base:      1 * time.Second,
			max:       5 * time.Second,
			count:     0,
			wantPanic: true,
		},
		{
			name:    "must_be_retry_after_value_if_parsable",
			header:  http.Header{"Retry-After": []string{"1"}},
			base:    1 * time.Second,
			max:     5 * time.Second,
			count:   1,
			wantMin: 1 * time.Second,
			wantMax: 1 * time.Second,
		},
		{
			name:    "must_be_exponential_with_jitter_if_retry_value_not_parseable",
			header:  http.Header{"Retry-After": []string{crand.Text()}},
			base:    1 * time.Second,
			max:     5 * time.Second,
			count:   1,
			wantMin: 1*time.Second - (1 * time.Second / 10),
			wantMax: 1*time.Second + (1 * time.Second / 10),
		},
		{
			name:    "must_be_exponential_with_jitter_and_respect_max_clamp",
			header:  http.Header{},
			base:    1 * time.Second,
			max:     5 * time.Second,
			count:   4, // 2^3 = 8 seconds, but clamp to 5
			wantMin: 5*time.Second - (5 * time.Second / 10),
			wantMax: 5*time.Second + (5 * time.Second / 10),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == tt.wantPanic {
					t.Errorf("did not panic as expected")
				}
			}()

			resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: tt.header}

			got := delay(resp, tt.base, tt.max, tt.count)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("got duration %v, want > %v and < %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestRetryAndObserve(t *testing.T) {
	type response struct {
		status     int
		err        error
		retryAfter string
	}

	tests := []struct {
		name      string
		method    string
		delayBase time.Duration
		delayMax  time.Duration
		maxTries  uint
		breaker   *CircuitBreaker

		//request options
		idempotencyKey string
		cancelAfter    *time.Duration

		responses  []response
		wantTries  uint
		wantErr    bool
		wantStatus int
		wantBody   string
	}{
		{
			name:     "must_error_on_breaker_not_ok",
			method:   http.MethodGet,
			maxTries: 3,
			breaker: &CircuitBreaker{
				failures:  0,
				threshold: 3,
				cooldown:  10 * time.Second,
				showtime:  time.Now().Add(10 * time.Second).UTC(),
				state:     BreakerStateOpen,
			},
			responses: []response{
				{status: http.StatusOK},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries: 0, // observe the underlying roundtrip never even tried
			wantErr:   true,
		},
		{
			name:        "must_error_after_context_cancelled_immediately",
			method:      http.MethodGet,
			delayBase:   20 * time.Millisecond,
			delayMax:    40 * time.Millisecond,
			maxTries:    3,
			cancelAfter: new(time.Duration(0)),
			responses: []response{
				{status: http.StatusInternalServerError},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries: 1,
			wantErr:   true,
		},
		{
			name:        "must_error_after_context_cancelled_during_delay",
			method:      http.MethodGet,
			delayBase:   20 * time.Millisecond,
			delayMax:    40 * time.Millisecond,
			maxTries:    3,
			cancelAfter: new(2 * time.Millisecond),
			responses: []response{
				{status: http.StatusInternalServerError},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries: 1,
			wantErr:   true,
		},
		{
			name:     "must_error_after_error_and_req_method_patch_not_idempotent",
			method:   http.MethodPatch,
			maxTries: 3,
			responses: []response{
				{err: fmt.Errorf("simulated network error")},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries: 1,
			wantErr:   true,
		},
		{
			name:     "must_error_after_error_and_req_method_post_not_idempotent",
			method:   http.MethodPost,
			maxTries: 3,
			responses: []response{
				{err: fmt.Errorf("simulated network error")},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries: 1,
			wantErr:   true,
		},
		{
			name:           "must_retry_on_req_method_patch_idempotent",
			method:         http.MethodPatch,
			maxTries:       3,
			idempotencyKey: strconv.Itoa(rand.IntN(100)),
			responses: []response{
				{err: fmt.Errorf("simulated network error")},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries:  2,
			wantErr:    false,
			wantStatus: http.StatusOK,
			wantBody:   http.StatusText(http.StatusOK),
		},
		{
			name:           "must_retry_on_req_method_post_idempotent",
			method:         http.MethodPost,
			maxTries:       3,
			idempotencyKey: strconv.Itoa(rand.IntN(100)),
			responses: []response{
				{err: fmt.Errorf("simulated network error")},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries:  2,
			wantErr:    false,
			wantStatus: http.StatusOK,
			wantBody:   http.StatusText(http.StatusOK),
		},
		{
			name:     "must_retry_on_resp_gt_500_lt_max_tries",
			method:   http.MethodGet,
			maxTries: 3,
			responses: []response{
				{status: http.StatusInternalServerError},
				{status: http.StatusInternalServerError},
				{status: http.StatusOK},
			},
			wantTries:  3,
			wantErr:    false,
			wantStatus: http.StatusOK,
			wantBody:   http.StatusText(http.StatusOK),
		},
		{
			name:     "must_retry_on_resp_eq_429_lt_max_tries",
			method:   http.MethodGet,
			maxTries: 3,
			responses: []response{{
				status: http.StatusTooManyRequests},
				{status: http.StatusTooManyRequests},
				{status: http.StatusOK},
			},
			wantTries:  3,
			wantErr:    false,
			wantStatus: http.StatusOK,
			wantBody:   http.StatusText(http.StatusOK),
		},
		{
			name:     "must_retry_and_respect_retry_after_delay",
			method:   http.MethodGet,
			maxTries: 3,
			responses: []response{{
				status: http.StatusTooManyRequests},
				{status: http.StatusTooManyRequests, retryAfter: "1"},
				{status: http.StatusOK},
			},
			wantTries:  3,
			wantErr:    false,
			wantStatus: http.StatusOK,
			wantBody:   http.StatusText(http.StatusOK),
		},
		{
			name:     "must_be_ok_with_last_resp_after_failed_max_tries",
			method:   http.MethodGet,
			maxTries: 3,
			responses: []response{
				{status: http.StatusInternalServerError},
				{status: http.StatusInternalServerError},
				{status: http.StatusInternalServerError},
			},
			wantTries:  3,
			wantErr:    false,
			wantStatus: http.StatusInternalServerError,
			wantBody:   http.StatusText(http.StatusInternalServerError),
		},
		{
			name:     "must_be_ok_on_resp_status_not_gt_500_nor_eq_429",
			method:   http.MethodGet,
			maxTries: 3,
			responses: []response{
				{status: http.StatusBadRequest},
				{status: http.StatusOK},
				{status: http.StatusOK},
			},
			wantTries:  1,
			wantErr:    false,
			wantStatus: http.StatusBadRequest,
			wantBody:   http.StatusText(http.StatusBadRequest),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tries uint = 0

			tripper := RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if tries >= tt.maxTries {
					t.Fatalf("max calls exceeded")
				}

				re := tt.responses[tries]
				tries++

				if re.err != nil {
					return nil, re.err
				}

				resp := &http.Response{
					StatusCode: re.status,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader(http.StatusText(re.status))),
					Request:    r,
				}

				if re.retryAfter != "" {
					resp.Header.Set("Retry-After", re.retryAfter)
				}

				return resp, nil
			})

			if tt.delayBase == 0 {
				tt.delayBase = 1 * time.Millisecond
			}

			if tt.delayMax == 0 {
				tt.delayMax = 10 * time.Millisecond
			}

			retry := RetryAndObserve(tt.maxTries, tt.delayBase, tt.delayMax, tt.breaker, nil)(tripper)

			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancelAfter != nil {
				if *tt.cancelAfter == 0 {
					cancel()
				} else {
					go func() {
						time.Sleep(*tt.cancelAfter)
						cancel()
					}()
				}
			}

			req, err := http.NewRequestWithContext(ctx, tt.method, "/", nil)
			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}

			if err != nil {
				t.Fatalf("failed to create request")
			}
			resp, err := retry.RoundTrip(req)

			if tries != tt.wantTries {
				t.Errorf("got tries %v, want %v", tries, tt.wantTries)
			}
			if got := (err != nil); got != tt.wantErr {
				t.Errorf("got err %v, want %v", got, tt.wantErr)
			}
			if err == nil {
				if got := resp.StatusCode; got != tt.wantStatus {
					t.Errorf("got status %v, want %v", got, tt.wantStatus)
				}
				defer resp.Body.Close()
				got, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read response body")
				}
				if string(got) != tt.wantBody {
					t.Errorf("got body '%v', want '%v'", string(got), tt.wantBody)
				}
			}
		})
	}
}
