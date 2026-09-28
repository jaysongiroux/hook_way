// promrule2groups extracts PrometheusRule spec.groups for promtool.
// Invocation: go run ./scripts/promrule2groups <prometheusrule.yaml>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: promrule2groups <prometheusrule.yaml>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(filepath.Clean(os.Args[1]))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	spec, _ := doc["spec"].(map[string]any)
	if spec == nil {
		fmt.Fprintln(os.Stderr, "prometheusrule: missing spec")
		os.Exit(1)
	}
	out, err := yaml.Marshal(map[string]any{"groups": spec["groups"]})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
