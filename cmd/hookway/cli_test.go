package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

var commandNames = []string{"serve", "validate", "schema", "editor", "healthcheck", "version"}

func TestHelpListsEveryCommand(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		code, stdout, stderr := runCLI(args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v: code %d stderr %q", args, code, stderr)
		}
		for _, name := range commandNames {
			if !strings.Contains(stdout, "\n  "+name+" ") {
				t.Errorf("%v does not list %s:\n%s", args, name, stdout)
			}
		}
		if !strings.Contains(stdout, "HOOKWAY_CONFIG") || !strings.Contains(stdout, "HOOKWAY_LOG_LEVEL") {
			t.Errorf("%v does not name the environment variables:\n%s", args, stdout)
		}
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	for _, name := range commandNames {
		code, viaFlag, stderr := runCLI(name, "-h")
		if code != 0 || stderr != "" || !strings.HasPrefix(viaFlag, "hookway "+name+" — ") {
			t.Fatalf("%s -h: code %d stderr %q stdout %q", name, code, stderr, viaFlag)
		}
		if code, viaHelp, _ := runCLI("help", name); code != 0 || viaHelp != viaFlag {
			t.Errorf("help %s differs from %s -h:\n%s\n---\n%s", name, name, viaHelp, viaFlag)
		}
	}
}

// A typo must not fall through to serving.
func TestAnUnknownCommandIsAUsageError(t *testing.T) {
	code, stdout, stderr := runCLI("valdate")
	if code != 2 || stdout != "" || !strings.Contains(stderr, `unknown command "valdate"`) {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := runCLI("help", "valdate"); code != 2 || !strings.Contains(stderr, `unknown command "valdate"`) {
		t.Fatalf("help valdate: code %d stderr %q", code, stderr)
	}
}

// `hookway validate config.yaml` without --config used to validate
// $HOOKWAY_CONFIG instead, or nothing, and say so only indirectly.
func TestAStrayArgumentIsAUsageError(t *testing.T) {
	code, _, stderr := runCLI("validate", "examples/config.yaml")
	if code != 2 || !strings.Contains(stderr, `unexpected argument "examples/config.yaml"`) {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServeIsTheDefaultCommand(t *testing.T) {
	for _, args := range [][]string{{"serve", "--version"}, {"--version"}} {
		if code, stdout, _ := runCLI(args...); code != 0 || stdout != "hookway dev (none)\n" {
			t.Fatalf("%v: code %d stdout %q", args, code, stdout)
		}
	}
}

// flagsIn lists the flags a command's help page documents.
func flagsIn(help string) []string {
	_, section, ok := strings.Cut(help, "\nFlags:\n")
	if !ok {
		return nil
	}
	section, _, _ = strings.Cut(section, "\n\n")
	return regexp.MustCompile(`(?m)^  (--[a-z0-9-]+)`).FindAllString(section, -1)
}

// docSections splits a markdown file into its "### " sections by heading.
func docSections(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]string{}
	for _, part := range strings.Split("\n"+string(raw), "\n### ")[1:] {
		heading, body, _ := strings.Cut(part, "\n")
		sections[strings.TrimSpace(heading)] = body
	}
	return sections
}

// docs/cli.md has a section for every command, and each section names every
// flag the command's help lists, so neither can drift from the binary.
func TestCLIDocsCoverEveryCommandAndFlag(t *testing.T) {
	sections := docSections(t, "../../docs/cli.md")
	for _, name := range commandNames {
		body, ok := sections["hookway "+name]
		if !ok {
			t.Errorf("docs/cli.md has no \"### hookway %s\" section", name)
			continue
		}
		_, help, _ := runCLI(name, "-h")
		for _, f := range flagsIn(help) {
			if !strings.Contains(body, "`"+strings.TrimSpace(f)) {
				t.Errorf("docs/cli.md § hookway %s does not document %s", name, strings.TrimSpace(f))
			}
		}
	}
}
