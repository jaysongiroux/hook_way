package retry

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNextBackoffAppliesJitterWithinBand(t *testing.T) {
	for _, base := range []time.Duration{100 * time.Millisecond, time.Second, 5 * time.Second} {
		lower := time.Duration(float64(base) * 2 * 0.8)
		upper := time.Duration(float64(base) * 2 * 1.2)
		for i := 0; i < 200; i++ {
			got := NextBackoff(base, 0)
			if got < lower || got > upper {
				t.Fatalf("NextBackoff(%v, 0) = %v, want within [%v, %v]", base, got, lower, upper)
			}
		}
	}
}

func TestNextBackoffRespectsCapAndFloor(t *testing.T) {
	for i := 0; i < 200; i++ {
		if got := NextBackoff(time.Hour, 2*time.Second); got > 2*time.Second {
			t.Fatalf("NextBackoff exceeded cap: %v", got)
		}
		if got := NextBackoff(0, 0); got < BackoffFloor {
			t.Fatalf("NextBackoff(0, 0) = %v, want >= %v", got, BackoffFloor)
		}
	}
}

func TestAttemptsNormalizesToAtLeastOne(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{{-5, 1}, {0, 1}, {1, 1}, {4, 4}} {
		if got := (Policy{MaxAttempts: tc.in}).Attempts(); got != tc.want {
			t.Fatalf("Policy{MaxAttempts: %d}.Attempts() = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseRetryAfterSecondsAndHTTPDate(t *testing.T) {
	if got := ParseRetryAfter("2", time.Now()); got != 2*time.Second {
		t.Fatalf("ParseRetryAfter(\"2\") = %v, want 2s", got)
	}
	now := time.Now()
	header := now.Add(5 * time.Second).UTC().Format(http.TimeFormat)
	got := ParseRetryAfter(header, now)
	if got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("ParseRetryAfter(%q) = %v, want ~5s", header, got)
	}
}

func TestParseRetryAfterFallsBackToZero(t *testing.T) {
	for _, header := range []string{"", "   ", "soon", "-3", "0", "Mon, 99 Xxx 9999"} {
		if got := ParseRetryAfter(header, time.Now()); got != 0 {
			t.Fatalf("ParseRetryAfter(%q) = %v, want 0 (fall back to computed backoff)", header, got)
		}
	}
}

func TestSleepReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); err == nil {
		t.Fatal("Sleep on a cancelled context returned nil")
	}
	if err := Sleep(context.Background(), 0); err != nil {
		t.Fatalf("Sleep(ctx, 0) = %v, want nil", err)
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 599} {
		if !RetryableStatus(code) {
			t.Fatalf("RetryableStatus(%d) = false, want true", code)
		}
	}
	for _, code := range []int{200, 204, 301, 400, 401, 404, 409, 422, 600} {
		if RetryableStatus(code) {
			t.Fatalf("RetryableStatus(%d) = true, want false", code)
		}
	}
}
