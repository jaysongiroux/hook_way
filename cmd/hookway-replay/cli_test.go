package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var commandNames = []string{"serve", "migrate", "gc", "validate", "healthcheck", "version"}

func TestHelpListsEveryCommand(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		code, stdout, stderr := runCmd(args...)
		if code != 0 || stderr != "" {
			t.Fatalf("%v: code %d stderr %q", args, code, stderr)
		}
		for _, name := range commandNames {
			if !strings.Contains(stdout, "\n  "+name+" ") {
				t.Errorf("%v does not list %s:\n%s", args, name, stdout)
			}
		}
		for _, env := range []string{"HOOKWAY_REPLAY_CONFIG", "HOOKWAY_LOG_LEVEL"} {
			if !strings.Contains(stdout, env) {
				t.Errorf("%v does not name %s:\n%s", args, env, stdout)
			}
		}
	}
}

// With no command there is nothing to default to: the command list goes to
// stderr with a usage exit.
func TestNoCommandPrintsTheListAsAUsageError(t *testing.T) {
	code, stdout, stderr := runCmd()
	if code != 2 || stdout != "" || !strings.Contains(stderr, "hookway-replay — ") || !strings.Contains(stderr, "\n  migrate ") {
		t.Fatalf("code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	for _, name := range commandNames {
		code, viaFlag, stderr := runCmd(name, "-h")
		if code != 0 || stderr != "" || !strings.HasPrefix(viaFlag, "hookway-replay "+name+" — ") {
			t.Fatalf("%s -h: code %d stderr %q stdout %q", name, code, stderr, viaFlag)
		}
		if code, viaHelp, _ := runCmd("help", name); code != 0 || viaHelp != viaFlag {
			t.Errorf("help %s differs from %s -h:\n%s\n---\n%s", name, name, viaHelp, viaFlag)
		}
	}
	if code, _, stderr := runCmd("help", "nope"); code != 2 || !strings.Contains(stderr, `unknown command "nope"`) {
		t.Fatalf("help nope: code %d stderr %q", code, stderr)
	}
}

func TestAStrayArgumentIsAUsageError(t *testing.T) {
	code, _, stderr := runCmd("migrate", "replay.yaml")
	if code != 2 || !strings.Contains(stderr, `unexpected argument "replay.yaml"`) {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func flagsIn(help string) []string {
	_, section, ok := strings.Cut(help, "\nFlags:\n")
	if !ok {
		return nil
	}
	section, _, _ = strings.Cut(section, "\n\n")
	return regexp.MustCompile(`(?m)^  (--[a-z0-9-]+)`).FindAllString(section, -1)
}

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

// docs/reference/cli.md has a section for every command, and each section names every
// flag the command's help lists.
func TestCLIDocsCoverEveryCommandAndFlag(t *testing.T) {
	sections := docSections(t, "../../docs/reference/cli.md")
	for _, name := range commandNames {
		body, ok := sections["hookway-replay "+name]
		if !ok {
			t.Errorf("docs/reference/cli.md has no \"### hookway-replay %s\" section", name)
			continue
		}
		_, help, _ := runCmd(name, "-h")
		for _, f := range flagsIn(help) {
			if !strings.Contains(body, "`"+strings.TrimSpace(f)) {
				t.Errorf("docs/reference/cli.md § hookway-replay %s does not document %s", name, strings.TrimSpace(f))
			}
		}
	}
}
