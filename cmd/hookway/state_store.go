package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/jaysongiroux/hook_way/internal/cli"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/protectionstore"
)

func runStateStore(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("state-store", flag.ContinueOnError)
	path := flags.String("config", "", "configuration FILE (default $HOOKWAY_CONFIG)")
	name := flags.String("store", "", "named state STORE (required)")
	id := flags.String("policy", "", "policy ID for a coordinated update")
	expected := flags.String("expected-fingerprint", "", "current policy HASH for compare-and-swap")
	timeout := flags.Duration("timeout", 30*time.Second, "overall administrative operation budget")
	action := ""
	if len(args) > 0 && args[0] != "-h" && args[0] != "--help" && args[0] != "-help" {
		action = args[0]
		args = args[1:]
	}
	if code, ok := program.Parse("state-store", flags, args, stdout, stderr); !ok {
		return code
	}
	if action != "migrate" && action != "status" && action != "gc" && action != "policy-update" {
		writef(stderr, "state-store requires migrate, status, gc, or policy-update\n")
		return cli.ExitUsage
	}
	if *name == "" || *timeout < 100*time.Millisecond || *timeout > 5*time.Minute {
		writef(stderr, "state-store requires --store; --timeout must be 100ms..5m\n")
		return cli.ExitUsage
	}
	if action == "policy-update" {
		decoded, err := hex.DecodeString(*expected)
		if *id == "" || err != nil || len(decoded) != 32 {
			writef(stderr, "policy-update requires --policy and a 64-character --expected-fingerprint\n")
			return cli.ExitUsage
		}
	} else if *id != "" || *expected != "" {
		writef(stderr, "--policy and --expected-fingerprint apply only to policy-update\n")
		return cli.ExitUsage
	}
	resolvedPath, pathErr := config.ResolvePath(*path)
	if pathErr != nil {
		writef(stderr, "state-store config required: use --config or HOOKWAY_CONFIG\n")
		return cli.ExitUsage
	}
	cfg, issues := flowconfig.Load(resolvedPath)
	for _, issue := range issues {
		if issue.Severity != "warning" {
			writef(stderr, "state-store configuration invalid; run hookway validate\n")
			return cli.ExitFail
		}
	}
	storeCfg, ok := cfg.StateStores[*name]
	if !ok {
		writef(stderr, "state-store is not configured\n")
		return cli.ExitFail
	}
	if storeCfg.Memory != nil {
		writef(stderr, "memory state is private to the serving process; live administration requires redis or postgres\n")
		return cli.ExitFail
	}
	policies, err := flowconfig.ProtectionPolicies(cfg)
	if err != nil {
		writef(stderr, "state-store policies invalid; run hookway validate\n")
		return cli.ExitFail
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var store protection.Store
	if storeCfg.Postgres != nil {
		store, err = protectionstore.OpenPostgres(ctx, storeCfg)
	} else {
		store, err = protectionstore.Open(ctx, storeCfg)
	}
	if err != nil {
		writef(stderr, "state-store unavailable; verify connection, permissions, and migrations\n")
		return cli.ExitFail
	}
	defer func() { _ = store.Close() }()
	fail := func(err error) int { writef(stderr, "state-store: %v\n", err); return cli.ExitFail }
	if action == "migrate" {
		pg, ok := store.(*protectionstore.Postgres)
		if !ok {
			writef(stderr, "migrate requires a postgres state store\n")
			return cli.ExitUsage
		}
		versions, err := pg.Migrate(ctx)
		if err != nil {
			return fail(err)
		}
		if err = pg.Check(ctx); err != nil {
			return fail(err)
		}
		writef(stdout, "state-store migrations applied: %v\n", versions)
		return cli.ExitOK
	}
	var schemaVersion *int64
	if pg, ok := store.(*protectionstore.Postgres); ok {
		v, err := pg.SchemaVersion(ctx)
		if err != nil {
			return fail(err)
		}
		schemaVersion = &v
		if v != protectionstore.LatestSchemaVersion() && action != "status" {
			return fail(fmt.Errorf("schema version %d is incompatible; run hookway state-store migrate", v))
		}
	}
	if action == "status" {
		type policyStatus struct {
			ID                 string             `json:"id"`
			DesiredFingerprint string             `json:"desired_fingerprint"`
			Missing            bool               `json:"missing,omitempty"`
			Actual             *protection.Status `json:"actual,omitempty"`
		}
		result := struct {
			Store                 string         `json:"store"`
			SchemaVersion         *int64         `json:"schema_version,omitempty"`
			RequiredSchemaVersion int64          `json:"required_schema_version,omitempty"`
			Policies              []policyStatus `json:"policies"`
		}{Store: *name, SchemaVersion: schemaVersion, Policies: []policyStatus{}}
		if schemaVersion != nil {
			result.RequiredSchemaVersion = protectionstore.LatestSchemaVersion()
		}
		for _, p := range policies[*name] {
			entry := policyStatus{ID: p.ID, DesiredFingerprint: p.Fingerprint()}
			if schemaVersion == nil || *schemaVersion == protectionstore.LatestSchemaVersion() {
				actual, err := store.PolicyStatus(ctx, p.ID)
				if errors.Is(err, protection.ErrPolicyConflict) {
					entry.Missing = true
				} else if err != nil {
					return fail(err)
				} else {
					entry.Actual = &actual
				}
			} else {
				entry.Missing = true
			}
			result.Policies = append(result.Policies, entry)
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			writef(stderr, "state-store: cannot encode status\n")
			return cli.ExitFail
		}
		return cli.ExitOK
	}
	if err = store.Check(ctx); err != nil {
		return fail(err)
	}
	if action == "policy-update" {
		for _, p := range policies[*name] {
			if p.ID == *id {
				if err = store.UpdatePolicy(ctx, p, *expected); err != nil {
					return fail(err)
				}
				writef(stdout, "policy updated: %s fingerprint=%s\n", p.ID, p.Fingerprint())
				return cli.ExitOK
			}
		}
		writef(stderr, "policy ID is not used by the selected store in this config\n")
		return cli.ExitFail
	}
	// Discover configured policies read-only; Redis never uses EnsurePolicy
	// during administration, and GC also supports a stale configured version.
	for _, p := range policies[*name] {
		if _, err = store.PolicyStatus(ctx, p.ID); err != nil && !errors.Is(err, protection.ErrPolicyConflict) {
			return fail(err)
		}
	}
	removed, err := store.GC(ctx)
	if err != nil {
		return fail(err)
	}
	writef(stdout, "state-store expired entries removed: %d\n", removed)
	return cli.ExitOK
}
