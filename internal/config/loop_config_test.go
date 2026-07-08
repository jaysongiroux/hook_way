package config

import (
	"strings"
	"testing"
)

func TestLoopProtectionDefaults(t *testing.T) {
	var s SecurityConfig
	if !s.LoopProtectionEnabled() {
		t.Fatal("loop protection should default to enabled")
	}
	if s.LoopMaxHops() != defaultLoopMaxHops {
		t.Fatalf("default max hops = %d, want %d", s.LoopMaxHops(), defaultLoopMaxHops)
	}
}

func TestLoopProtectionExplicitDisableAndMax(t *testing.T) {
	disabled := false
	s := SecurityConfig{LoopProtection: LoopProtectionConfig{Enabled: &disabled, MaxHops: 7}}
	if s.LoopProtectionEnabled() {
		t.Fatal("loop protection should be disabled when enabled=false")
	}
	if s.LoopMaxHops() != 7 {
		t.Fatalf("max hops = %d, want 7", s.LoopMaxHops())
	}
}

func TestLoadParsesLoopProtection(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), "security:\n", `security:
  loop_protection:
    enabled: true
    max_hops: 5
`, 1)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.Security.LoopProtectionEnabled() || cfg.Security.LoopMaxHops() != 5 {
		t.Fatalf("loop protection = %#v", cfg.Security.LoopProtection)
	}
}

func TestLoadRejectsNegativeMaxHops(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), "security:\n", `security:
  loop_protection:
    max_hops: -1
`, 1)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error for negative max_hops")
	}
	if !strings.Contains(err.Error(), "security.loop_protection.max_hops") {
		t.Fatalf("unexpected error: %v", err)
	}
}
