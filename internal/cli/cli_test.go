package cli

import (
	"bytes"
	"flag"
	"strings"
	"testing"
	"time"
)

var program = Program{
	Name:    "tool",
	Summary: "does things",
	Default: "serve",
	Commands: []Command{
		{Name: "serve", Args: "[--config FILE]", Summary: "serve things", Help: "Serves until SIGTERM.",
			Examples: []string{"tool serve --config tool.yaml"}},
		{Name: "check", Args: "[--config FILE] [--strict]", Summary: "check a config", Help: "Checks it."},
	},
	Footer: "Environment:\n  TOOL_CONFIG  default for --config",
}

func checkFlags() *flag.FlagSet {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.String("config", "", "`FILE` to check (default $TOOL_CONFIG)")
	flags.Bool("strict", false, "treat warnings as errors")
	flags.Duration("timeout", 5*time.Second, "give up after `DURATION`")
	return flags
}

func parse(args ...string) (code int, ok bool, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code, ok = program.Parse("check", checkFlags(), args, &out, &errOut)
	return code, ok, out.String(), errOut.String()
}

func TestProgramHelpListsEveryCommand(t *testing.T) {
	var out bytes.Buffer
	program.WriteHelp(&out)
	help := out.String()
	for _, want := range []string{
		"tool — does things", "tool <command> [flags]", "tool [flags]", "runs serve",
		"serve", "serve things", "check", "check a config", "help",
		"tool help <command>", "TOOL_CONFIG",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("program help lacks %q:\n%s", want, help)
		}
	}
}

func TestProgramWithoutADefaultHasOneUsageLine(t *testing.T) {
	p := program
	p.Default = ""
	var out bytes.Buffer
	p.WriteHelp(&out)
	if strings.Contains(out.String(), "runs serve") {
		t.Fatalf("help names a default command it does not have:\n%s", out.String())
	}
}

func TestDashHPrintsCommandHelpToStdout(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		code, ok, stdout, stderr := parse(arg)
		if ok || code != ExitOK || stderr != "" {
			t.Fatalf("%s: code %d ok %v stderr %q", arg, code, ok, stderr)
		}
		for _, want := range []string{
			"tool check — check a config", "Usage:\n  tool check [--config FILE] [--strict]", "Checks it.", "Flags:",
			"--config FILE", "FILE to check (default $TOOL_CONFIG)", "--strict ", "--timeout DURATION",
			"(default 5s)",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s: help lacks %q:\n%s", arg, want, stdout)
			}
		}
		if strings.Contains(stdout, "(default false)") {
			t.Errorf("a bool flag's false default is noise:\n%s", stdout)
		}
	}
}

func TestExamplesAppearInCommandHelp(t *testing.T) {
	var out bytes.Buffer
	program.WriteCommandHelp(&out, program.Commands[0], flag.NewFlagSet("serve", flag.ContinueOnError))
	if !strings.Contains(out.String(), "Examples:\n  tool serve --config tool.yaml") {
		t.Fatalf("examples missing:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Flags:") {
		t.Fatalf("a command with no flags prints an empty Flags section:\n%s", out.String())
	}
}

func TestABadFlagIsAUsageError(t *testing.T) {
	code, ok, stdout, stderr := parse("--nope")
	if ok || code != ExitUsage || stdout != "" {
		t.Fatalf("code %d ok %v stdout %q", code, ok, stdout)
	}
	for _, want := range []string{"tool check: flag provided but not defined: -nope", "Run 'tool help check' for usage."} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}

// `tool check tool.yaml` (forgetting --config) must not quietly check the
// default config instead.
func TestAStrayArgumentIsAUsageError(t *testing.T) {
	code, ok, _, stderr := parse("--strict", "tool.yaml")
	if ok || code != ExitUsage || !strings.Contains(stderr, `tool check: unexpected argument "tool.yaml"`) {
		t.Fatalf("code %d ok %v stderr %q", code, ok, stderr)
	}
}

func TestGoodFlagsParse(t *testing.T) {
	code, ok, stdout, stderr := parse("--config", "x.yaml", "--strict")
	if !ok || code != ExitOK || stdout != "" || stderr != "" {
		t.Fatalf("code %d ok %v stdout %q stderr %q", code, ok, stdout, stderr)
	}
}

func TestLookup(t *testing.T) {
	if c, ok := program.Lookup("check"); !ok || c.Summary != "check a config" {
		t.Fatalf("Lookup(check) = %+v, %v", c, ok)
	}
	if _, ok := program.Lookup("nope"); ok {
		t.Fatal("Lookup(nope) found a command")
	}
}

func TestUnknownCommandPointsAtHelp(t *testing.T) {
	var errOut bytes.Buffer
	if code := program.UnknownCommand(&errOut, "chek"); code != ExitUsage {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{`tool: unknown command "chek"`, "Run 'tool help' for usage."} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, errOut.String())
		}
	}
}
