// Package retry holds the shared backoff and Retry-After primitives used by
// outbound delivery paths. It imports nothing outside the standard library so
// it can be linked into the GOOS=js/GOARCH=wasm config validator alongside
// internal/flowconfig (see Makefile target "wasm").
package retry

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BackoffFloor is the smallest delay NextBackoff will ever return.
const BackoffFloor = 100 * time.Millisecond

// Policy is a bounded exponential-backoff retry budget. A zero Policy means
// "one attempt, no retries".
type Policy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Attempts is MaxAttempts clamped to a minimum of one, so a zero or negative
// budget still performs the initial attempt.
func (p Policy) Attempts() int {
	if p.MaxAttempts < 1 {
		return 1
	}
	return p.MaxAttempts
}

// NextBackoff doubles delay (exponential backoff), applies +/-20% jitter so
// many callers backing off after a shared outage don't retry in lockstep, then
// re-applies the floor and cap so jitter can never push the result below the
// floor or above a configured maxDelay. This is a runtime-path random source
// (not config/security sensitive), so math/rand/v2 is fine.
func NextBackoff(delay, maxDelay time.Duration) time.Duration {
	if delay <= 0 {
		delay = BackoffFloor
	}
	delay *= 2
	delay = jitter(delay)
	if delay < BackoffFloor {
		delay = BackoffFloor
	}
	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

// jitter multiplies d by a uniformly random factor in [0.8, 1.2) (+/-20%).
func jitter(d time.Duration) time.Duration {
	factor := 0.8 + rand.Float64()*0.4 // #nosec G404 -- retry jitter, not a credential
	return time.Duration(float64(d) * factor)
}

// Sleep waits for delay or until ctx is done, whichever comes first. It
// returns ctx.Err() when the wait was cut short.
func Sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ParseRetryAfter parses a Retry-After header per RFC 9110 section 10.2.3:
// either an integer number of seconds, or an HTTP-date. It returns 0 — meaning
// "fall back to the computed backoff" — when the header is absent, malformed,
// or resolves to a non-positive delay.
func ParseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(header); err == nil {
		if d := when.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// RetryableStatus reports whether a response status is worth another attempt:
// 429 (rate limited) and any 5xx. Everything else — including every other 4xx
// — is terminal, because a rejected payload never becomes acceptable on a
// retry.
func RetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}
