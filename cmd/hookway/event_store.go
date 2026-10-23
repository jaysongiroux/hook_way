package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/eventdelivery"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

func runEventStore(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeln(stderr, "event-store requires migrate, status, or gc")
		return 1
	}
	action := args[0]
	if action == "-h" || action == "--help" {
		flags := flag.NewFlagSet("event-store", flag.ContinueOnError)
		_, _ = program.Parse("event-store", flags, args, stdout, stderr)
		return 0
	}
	if action != "migrate" && action != "status" && action != "gc" {
		writeln(stderr, "event-store requires migrate, status, or gc")
		return 1
	}
	flags := flag.NewFlagSet("event-store", flag.ContinueOnError)
	file := flags.String("config", "", "configuration FILE")
	if code, ok := program.Parse("event-store", flags, args[1:], stdout, stderr); !ok {
		return code
	}
	path, err := config.ResolvePath(*file)
	if err != nil {
		writeln(stderr, "event-store requires a config")
		return 1
	}
	cfg, issues := flowconfig.Load(path)
	if len(blockingValidationIssues(issues)) > 0 || cfg.EventDelivery == nil {
		writeln(stderr, "event-store requires a valid event_delivery configuration")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := eventdelivery.Open(ctx, *cfg.EventDelivery)
	if err != nil {
		writeln(stderr, "event store unavailable")
		return 1
	}
	defer func() { _ = s.Close() }()
	switch action {
	case "migrate":
		err = s.Migrate(ctx)
	case "gc":
		err = s.GC(ctx)
	case "status":
		var version int64
		version, err = s.SchemaVersion(ctx)
		if err == nil {
			_, err = fmt.Fprintf(stdout, "event-store schema: %d (required: 1)\n", version)
		}
	}
	if err != nil {
		writeln(stderr, "event-store operation failed; check connection, permissions and migrations")
		return 1
	}
	if action != "status" {
		writef(stdout, "event-store %s complete\n", action)
	}
	return 0
}
