package config

import (
	"strings"
	"testing"
	"time"
)

func TestShutdownConfigurationBudgets(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	for _, tt := range []struct {
		name, extra string
		valid       bool
	}{
		{"default", "", true},
		{"withdrawal", "  shutdown_delay: 5s\n  shutdown_total_timeout: 60s\n", true},
		{"zero delay", "  shutdown_delay: 0s\n", true},
		{"negative delay", "  shutdown_delay: -1s\n", false},
		{"zero total", "  shutdown_total_timeout: 0s\n", false},
		{"no cleanup budget", "  shutdown_delay: 5s\n  shutdown_total_timeout: 15s\n", false},
		{"delay exhausts total", "  shutdown_delay: 1m\n  shutdown_total_timeout: 60s\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(validSharedYAML(), "server:\n", "server:\n"+tt.extra, 1)
			cfg, err := loadSharedYAML(t, body)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v error=%v", tt.valid, err)
			}
			if err == nil && cfg.Server.ShutdownTotalTimeout != time.Minute {
				t.Fatalf("total=%v", cfg.Server.ShutdownTotalTimeout)
			}
		})
	}
}

func TestOmittedTotalBudgetPreservesLongExistingDrain(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := strings.Replace(validSharedYAML(), `shutdown_timeout: "10s"`, `shutdown_timeout: "2m"`, 1)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ShutdownTotalTimeout != 150*time.Second {
		t.Fatalf("total=%v", cfg.Server.ShutdownTotalTimeout)
	}
	body = strings.Replace(body, "server:\n", "server:\n  shutdown_total_timeout: 60s\n", 1)
	if _, err := loadSharedYAML(t, body); err == nil {
		t.Fatal("explicit insufficient total was accepted")
	}
}

func TestShutdownDefaultDoesNotOverflow(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := strings.Replace(validSharedYAML(), "server:\n", "server:\n  shutdown_delay: 9223372036854775807ns\n", 1)
	if _, err := loadSharedYAML(t, body); err == nil || !strings.Contains(err.Error(), "server.shutdown_total_timeout") {
		t.Fatalf("overflowing combination: %v", err)
	}
}
