package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEventStoreCLIValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown"}, {"status", "--config", "/missing/config.yaml"}} {
		var out, err bytes.Buffer
		if runEventStore(args, &out, &err) == 0 {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	for _, args := range [][]string{{"-h"}, {"--help"}, {"status", "-h"}} {
		var out, err bytes.Buffer
		if runEventStore(args, &out, &err) != 0 {
			t.Fatal(err.String())
		}
		if !strings.Contains(out.String()+err.String(), "event-store") {
			t.Fatal("missing command help")
		}
	}
}
