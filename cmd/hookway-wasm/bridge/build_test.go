package bridge_test

import (
	"os"
	"os/exec"
	"testing"
)

// The wasm entrypoint must cross-compile for the browser target. This guards
// against syscall/js misuse and build-tag mistakes that normal `go test`
// (host GOOS) would never catch, because main.go is excluded on the host.
func TestWasmEntrypointCompiles(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", os.DevNull,
		"github.com/jasongiroux/hookway/cmd/hookway-wasm")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm build failed: %v\n%s", err, out)
	}
}
