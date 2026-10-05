// hookway-openapi deterministically generates the HTTP contracts from live route
// metadata and Go DTOs. --check verifies committed files without rewriting them.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jaysongiroux/hook_way/internal/editor"
	replayapi "github.com/jaysongiroux/hook_way/internal/replay/api"
)

func main() {
	out := flag.String("out", "docs/api", "output directory")
	check := flag.Bool("check", false, "check generated files without writing")
	flag.Parse()
	if err := generate(*out, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func generate(out string, check bool) error {
	for _, spec := range []struct {
		name   string
		render func() ([]byte, error)
	}{{"editor.openapi.json", editor.OpenAPI}, {"replay.openapi.json", replayapi.OpenAPI}} {
		data, err := spec.render()
		if err != nil {
			return err
		}
		path := filepath.Join(out, spec.name)
		if check {
			existing, err := os.ReadFile(path) // #nosec G304 -- output path is explicitly selected by the local CLI caller.
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, data) {
				return fmt.Errorf("generated API contract drift: %s (run go run ./cmd/hookway-openapi)", path)
			}
			continue
		}
		if err := os.MkdirAll(out, 0750); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return err
		}
	}
	return nil
}
