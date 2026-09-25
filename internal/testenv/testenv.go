// Package testenv has helpers for container-backed integration tests.
package testenv

import (
	"os"
	"testing"
)

// SkipIfShort skips a container-backed test under go test -short.
func SkipIfShort(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping container test in -short mode")
	}
}

// RequireContainer treats a failed container start as Fatal when
// HOOKWAY_REQUIRE_INTEGRATION=1 (CI), and Skip otherwise (local).
func RequireContainer(t testing.TB, err error, name string) {
	t.Helper()
	if err == nil {
		return
	}
	if os.Getenv("HOOKWAY_REQUIRE_INTEGRATION") == "1" {
		t.Fatalf("%s container required (HOOKWAY_REQUIRE_INTEGRATION=1): %v", name, err)
	}
	t.Skipf("%s unavailable: %v", name, err)
}
