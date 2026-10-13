package bridge_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The wasm entrypoint must cross-compile for the browser target. This guards
// against syscall/js misuse and build-tag mistakes that normal `go test`
// (host GOOS) would never catch, because main.go is excluded on the host.
func TestWasmEntrypointCompiles(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", os.DevNull, "github.com/jaysongiroux/hook_way/cmd/hookway-wasm") // #nosec G204 -- fixed module path
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm build failed: %v\n%s", err, out)
	}
}

// Pure browser validation must not bring native adapters or database drivers
// into its dependency graph, even if a future SDK happens to cross-compile.
func TestWasmDependencyBoundary(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "github.com/jaysongiroux/hook_way/cmd/hookway-wasm") // #nosec G204 -- fixed module path
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, forbidden := range []string{
			"github.com/jaysongiroux/hook_way/internal/queue",
			"github.com/jaysongiroux/hook_way/internal/redisconn",
			"github.com/jaysongiroux/hook_way/internal/replay/store",
			"github.com/redis/", "github.com/jackc/", "github.com/aws/aws-sdk-go",
			"go.mongodb.org/", "github.com/segmentio/kafka-go", "go.temporal.io/",
			"github.com/rabbitmq/",
		} {
			if strings.HasPrefix(dep, forbidden) {
				t.Errorf("native dependency in WASM: %s", dep)
			}
		}
	}
}
