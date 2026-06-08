package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunValidateAcceptsValidConfig(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_WEBHOOK_SECRET", "test-secret")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", filepath.Join("..", "..", "examples", "config.yaml")}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config valid") {
		t.Fatalf("stdout = %q, want config valid message", stdout.String())
	}
}

func TestRunValidateRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte("server:\n  addr: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run() code = 0, want nonzero")
	}
	if !strings.Contains(stderr.String(), "config validation failed") {
		t.Fatalf("stderr = %q, want validation failure", stderr.String())
	}
}
