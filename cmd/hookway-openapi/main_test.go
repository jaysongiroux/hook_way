package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedContractsAreCurrent(t *testing.T) {
	if err := generate("../../docs/api", true); err != nil {
		t.Fatal(err)
	}
}
func TestGenerateCheckDetectsDriftWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir, false); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "replay.openapi.json")
	drift := []byte("{}\n")
	if err := os.WriteFile(path, drift, 0600); err != nil {
		t.Fatal(err)
	}
	if err := generate(dir, true); err == nil {
		t.Fatal("missing drift failure")
	}
	got, err := os.ReadFile(path) // #nosec G304 -- path belongs to this test temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(drift) {
		t.Fatal("check rewrote file")
	}
}
