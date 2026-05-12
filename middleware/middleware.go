// Package middleware provides composable HTTP middleware for use with net/http.
// It includes utilities like panic recovery, access logging, request logging,
// and response inspection.
package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

// captureWriter wraps http.ResponseWriter and captures the HTTP status code
// written to the client. It is used internally by AccessLogger.
type captureWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status code and delegates to the underlying
// ResponseWriter. If statusCode is zero, http.StatusOK is assumed mimicking
// Go internals.
func (rw *captureWriter) WriteHeader(statusCode int) {
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	rw.status = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// AccessLogger returns middleware that logs request and server response
// details. Logged fields include remote address, method, URL, protocol,
// response status, user agent, and time to response (here as ttr).
//
// If logger is nil, slog.Default() is used.
func AccessLogger(logger *slog.Logger, prefix string) func(h http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &captureWriter{ResponseWriter: w}
			h.ServeHTTP(rw, r)
			duration := time.Since(start)

			logger.Info(
				prefix,
				slog.String("src", r.RemoteAddr),
				slog.String("method", r.Method),
				slog.String("dest", r.URL.RequestURI()),
				slog.String("proto", r.Proto),
				slog.Int("status", rw.status),
				slog.String("user-agent", r.Header.Get("User-Agent")),
				slog.Duration("ttr", duration),
			)
		})
	}
}

// RecoverAndHandle returns middleware that recovers from panics in downstream
// handlers. If a panic occurs, it logs the error and stack trace using logger,
// then delegates to the fallback handler.
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

// RoundTripperFunc implements RoundTripper. It may be used it to wrap another
// RoundTripper and act as middleware.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

// RequestLogger returns middleware that logs request and response round trips.
// Logged fields include remote address, method, URL, protocol, response status,
// user agent, and time to return (here as ttr).
//
// If logger is nil, slog.Default() is used.
func RequestLogger(logger *slog.Logger, prefix string) func(http.RoundTripper) http.RoundTripper {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			start := time.Now().UTC()
			resp, err := next.RoundTrip(r)
			duration := time.Since(start)

			logger.Info(
				prefix,
				slog.String("src", r.RemoteAddr),
				slog.String("method", r.Method),
				slog.String("dest", r.URL.String()),
				slog.String("proto", r.Proto),
				slog.Int("status", resp.StatusCode),
				slog.String("user-agent", r.Header.Get("User-Agent")),
				slog.Duration("ttr", duration),
			)

			return resp, err
		})
	}
}

// BreakerState represents a CircuitBreaker state
type BreakerState int

const (
	BreakerStateClosed BreakerState = iota
	BreakerStateOpen
)

// CircuitBreaker represents a simple two-state circuit. After threshold+1
// failures, the breaker breaks open and forces a cooldown period.
type CircuitBreaker struct {
	failures  uint
	threshold uint
	cooldown  time.Duration
	showtime  time.Time

	state BreakerState
	mutex sync.Mutex
}

// NewCircuitBreaker returns a new CircuitBreaker. It is initialized with a
// threshold and open cooldown time that cannot be changed. By default,
// the breaker is in the closed state and showtime is set to the current time.
func NewCircuitBreaker(threshold uint, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		showtime:  time.Now().UTC(),
	}
}

// State returns the current breaker's state. If open and past showtime, the
// breaker flips closed again.
func (breaker *CircuitBreaker) State() BreakerState {
	breaker.mutex.Lock()
	defer breaker.mutex.Unlock()

	if (breaker.state == BreakerStateOpen) && time.Now().UTC().After(breaker.showtime) {
		breaker.state = BreakerStateClosed
	}

	return breaker.state
}

// OK returns whether the circuit is closed and therefore ok.
func (breaker *CircuitBreaker) OK() bool { return breaker.State() == BreakerStateClosed }

// OnSuccess resets failures to zero.
func (breaker *CircuitBreaker) OnSuccess() {
	breaker.mutex.Lock()
	defer breaker.mutex.Unlock()

	breaker.failures = 0
}

// OnFailure increments failures. If failures exceeds threshold, the breaker
// breaks open, sets showtime to now + cooldown, and resets failures to 0.
func (breaker *CircuitBreaker) OnFailure() {
	breaker.mutex.Lock()
	defer breaker.mutex.Unlock()

	breaker.failures++

	if breaker.failures > breaker.threshold {

		breaker.showtime = time.Now().UTC().Add(breaker.cooldown)
		breaker.state = BreakerStateOpen
		breaker.failures = 0
	}
}

// RetryObserver is the interface implemented by an object that can observe
// retry behavior in a RetryAndObserve RoundTripper.
type RetryObserver interface {
	OnTry(*http.Request, uint)
	OnSuccess(*http.Request, uint)
	OnFailure(*http.Request, uint, error)
}

// NopRetryObserver is a no operation retry observer. It is used if no observer
// is supplied in RetryAndObserve
type NopRetryObserver struct{}

func (o *NopRetryObserver) OnTry(*http.Request, uint)            {}
func (o *NopRetryObserver) OnSuccess(*http.Request, uint)        {}
func (o *NopRetryObserver) OnFailure(*http.Request, uint, error) {}

func RetryAndObserve(tries uint, delayBase time.Duration, delayMax time.Duration, breaker *CircuitBreaker, observer RetryObserver) func(http.RoundTripper) http.RoundTripper {
	if observer == nil {
		observer = &NopRetryObserver{}
	}

	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if breaker != nil && !breaker.OK() {
				return nil, fmt.Errorf("circuit open: waiting until %v", breaker.showtime)
			}

			var err error
			var resp *http.Response

			var i uint
			for i = 1; i <= tries; i++ {
				observer.OnTry(r, i)

				// request must be cloned
				req := r.Clone(r.Context())
				resp, err = next.RoundTrip(req)

				// return on acceptable response
				if err == nil && resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
					if breaker != nil {
						breaker.OnSuccess()
					}
					observer.OnSuccess(req, i)
					return resp, nil
				}

				// skip retries if request is not idempotent
				// skip body discard if last try
				if !isIdempotent(req) || i == tries {
					break
				}

				// only reached on err, resp.StatusCode > 500 or resp.StatusCode == 429
				if resp != nil { // reset response body to prep for next try
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}

				delay := delay(resp, delayBase, delayMax, i)

				// and consider context
				select {
				case <-time.After(delay):
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}

			if breaker != nil {
				breaker.OnFailure()
			}
			observer.OnFailure(r, i, err)

			return resp, err
		})
	}
}

// isIdempotent returns true if any of the following apply:
//   - r.Method is canonically idempotent: GET, HEAD, OPTIONS, TRACE, PUT, or DELETE
//   - r.Header contains a non-empty header field "Idempotency-Key".
func isIdempotent(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodTrace,
		http.MethodPut,
		http.MethodDelete:
		return true
	}

	// allow any if explicitly marked safe
	if r.Header.Get("Idempotency-Key") != "" {
		return true
	}

	return false
}

// retryAfterValue returns time.Duration value, if any, from header key
// "Retry-After" in h.
func retryAfterValue(h http.Header) (time.Duration, bool) {
	v := h.Get("Retry-After")
	if v == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second, true
	}

	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}

	return 0, false
}

// delay returns an appropriate delay based on the current circumstaces.
// If a Retry-After header field value is present in the response, that is
// returned without further processing. Otherwise this returns an exponential
// backoff of base (clamped to max) with random jitter +-10% to prevent
// simultaneous retries. This will panic if base = 0, max = 0, or count = 0.
func delay(resp *http.Response, base time.Duration, max time.Duration, count uint) time.Duration {
	if resp != nil {
		if d, ok := retryAfterValue(resp.Header); ok {
			return d
		}
	}

	d := time.Duration(1<<(count-1)) * base
	if d > max {
		d = max
	}

	// -10%...+10%
	jitter := time.Duration(rand.Int64N(int64(d/5))) - d/10

	return d + jitter
}
