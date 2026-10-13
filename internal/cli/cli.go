// Package cli gives hookway and hookway-replay the same command-line
// conventions: a command list at `help`, full help for one command at
// `help <command>` or `<command> -h`, help on stdout with exit 0, and usage
// errors on stderr with exit 2 and a pointer to the help.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Exit codes shared by both commands.
const (
	ExitOK    = 0 // success, including printing help
	ExitFail  = 1 // the command ran and failed
	ExitUsage = 2 // the command line was wrong
)

// Command describes one subcommand for its help page.
type Command struct {
	Name     string   // as typed: "validate"
	Args     string   // the synopsis after the name: "[--config FILE] [--json]"
	Summary  string   // one line for the command list
	Help     string   // what it does, what it prints, and its exit codes; pre-wrapped
	Examples []string // shell lines
}

// Program describes a binary and its subcommands.
type Program struct {
	Name     string // "hookway"
	Summary  string // one line: what the binary is
	Default  string // the command run when none is given; empty if one is required
	Commands []Command
	Footer   string // environment and documentation pointers; pre-wrapped
}

// Lookup returns the named command.
func (p Program) Lookup(name string) (Command, bool) {
	for _, c := range p.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// WriteHelp prints the program's help: what it is, how to call it, and every
// command with its summary.
func (p Program) WriteHelp(w io.Writer) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n\nUsage:\n  %s <command> [flags]\n", p.Name, p.Summary, p.Name)
	if p.Default != "" {
		fmt.Fprintf(&b, "  %s [flags]    (runs %s)\n", p.Name, p.Default)
	}
	b.WriteString("\nCommands:\n")
	width := len("help")
	for _, c := range p.Commands {
		width = max(width, len(c.Name))
	}
	for _, c := range p.Commands {
		summary := c.Summary
		if c.Name == p.Default {
			summary += " (the default)"
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Name, summary)
	}
	fmt.Fprintf(&b, "  %-*s  %s\n", width, "help", "show help for a command")
	fmt.Fprintf(&b, "\nRun '%s help <command>' (or '%s <command> -h') for its flags and examples.\n", p.Name, p.Name)
	if p.Footer != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(p.Footer, "\n"))
	}
	_, _ = io.WriteString(w, b.String())
}

// WriteCommandHelp prints one command's help page, listing the flags defined
// on flags.
func (p Program) WriteCommandHelp(w io.Writer, c Command, flags *flag.FlagSet) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s — %s\n\nUsage:\n  %s %s", p.Name, c.Name, c.Summary, p.Name, c.Name)
	if c.Args != "" {
		b.WriteString(" " + c.Args)
	}
	b.WriteString("\n")
	if c.Help != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimRight(c.Help, "\n"))
	}
	if rows := flagRows(flags); len(rows) > 0 {
		b.WriteString("\nFlags:\n")
		width := 0
		for _, r := range rows {
			width = max(width, len(r[0]))
		}
		for _, r := range rows {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, r[0], r[1])
		}
	}
	if len(c.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range c.Examples {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	_, _ = io.WriteString(w, b.String())
}

// flagRows renders each flag as "--name ARG" and its usage. A backquoted word
// in the usage names the argument, as in the standard flag package.
func flagRows(flags *flag.FlagSet) [][2]string {
	var rows [][2]string
	flags.VisitAll(func(f *flag.Flag) {
		arg, usage := flag.UnquoteUsage(f)
		left := "--" + f.Name
		if arg != "" {
			left += " " + arg
		}
		if f.DefValue != "" && f.DefValue != "false" {
			usage += " (default " + f.DefValue + ")"
		}
		rows = append(rows, [2]string{left, usage})
	})
	return rows
}

// Parse parses a command's flags. When ok is false the caller returns code:
// ExitOK after -h or --help printed the command's help to stdout, or
// ExitUsage after a bad flag or a stray argument was reported on stderr.
func (p Program) Parse(name string, flags *flag.FlagSet, args []string, stdout, stderr io.Writer) (code int, ok bool) {
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	err := flags.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		c, _ := p.Lookup(name)
		c.Name = name
		p.WriteCommandHelp(stdout, c, flags)
		return ExitOK, false
	case err != nil:
		return p.usageError(stderr, name, err.Error()), false
	case flags.NArg() > 0:
		return p.usageError(stderr, name, fmt.Sprintf("unexpected argument %q", flags.Arg(0))), false
	}
	return ExitOK, true
}

// UnknownCommand reports a command name the program does not have.
func (p Program) UnknownCommand(stderr io.Writer, name string) int {
	_, _ = fmt.Fprintf(stderr, "%s: unknown command %q\nRun '%s help' for usage.\n", p.Name, name, p.Name)
	return ExitUsage
}

func (p Program) usageError(stderr io.Writer, name, msg string) int {
	_, _ = fmt.Fprintf(stderr, "%s %s: %s\nRun '%s help %s' for usage.\n", p.Name, name, msg, p.Name, name)
	return ExitUsage
}
