package config

import (
	"strings"
	"testing"
	"time"
)

func TestAdmissionWaitingConfiguration(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	for _, c := range []struct {
		extra string
		valid bool
	}{
		{"", true}, {"max_pending_requests: 64\n  admission_wait_timeout: 100ms", true},
		{"max_pending_requests: 64", false}, {"admission_wait_timeout: 100ms", false},
		{"max_pending_requests: -1", false}, {"max_pending_requests: 64\n  admission_wait_timeout: -1s", false},
		{"max_pending_requests: 64\n  admission_wait_timeout: 30s", false},
		{"max_pending_requests: 64\n  admission_wait_timeout: 100ms\n  max_inflight_requests: -1", false},
	} {
		t.Run(c.extra, func(t *testing.T) {
			input := strings.Replace(validSharedYAML(), "fanout_concurrency: 4", "fanout_concurrency: 4\n  "+c.extra, 1)
			cfg, err := loadSharedYAML(t, input)
			if (err == nil) != c.valid {
				t.Fatalf("valid=%v: %v", c.valid, err)
			}
			if c.valid && c.extra != "" && (cfg.Server.MaxPendingRequests != 64 || cfg.Server.AdmissionWaitTimeout != 100*time.Millisecond) {
				t.Fatalf("settings lost: %+v", cfg.Server)
			}
		})
	}
}
