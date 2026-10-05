package main

import "github.com/jaysongiroux/hook_way/internal/cli"

// program is hookway's help: `hookway help`, `hookway help <command>`, and
// `hookway <command> -h`. docs/reference/cli.md is the long form; a test keeps the two
// listing the same commands and flags.
var program = cli.Program{
	Name:    "hookway",
	Summary: "run the webhook ingress server and inspect flow configurations",
	Default: "serve",
	Commands: []cli.Command{
		{
			Name:    "state-store",
			Args:    "migrate|status|gc|policy-update --config FILE --store NAME [--timeout DURATION] [--policy ID --expected-fingerprint HASH]",
			Summary: "administer shared protection state",
			Help: `Requires a config (--config or HOOKWAY_CONFIG) and --store. migrate explicitly applies PostgreSQL migrations;
serving never migrates. status prints schema and actual/desired policy fingerprints
without registering policies. gc removes a bounded batch of expired state.

policy-update selects the desired policy from the config using --policy and performs
an explicit compare-and-swap with --expected-fingerprint from status. Quota state
and existing guard expiry survive compatible numeric changes. The policy fails
closed while bounded update batches run. An interrupted update can be resumed
using the same target config and original expected fingerprint. Identity/strategy
changes require an explicit state transition. The default operation budget is 30s.
Memory state is private to the server process and cannot be administered here.`,
			Examples: []string{"hookway state-store migrate --config core.yaml --store shared_pg", "hookway state-store status --config core.yaml --store shared_pg", "hookway state-store gc --config core.yaml --store shared_pg", "hookway state-store policy-update --config core.yaml --store shared_pg --policy quota --expected-fingerprint HASH"},
		},
		{
			Name:    "serve",
			Args:    "[--config FILE]",
			Summary: "serve webhooks",
			Help: `Loads and validates the config, connects every destination, and serves the
flows on server.addr until SIGINT or SIGTERM. Shutdown withdraws readiness for server.shutdown_delay, stops taking requests,
and drains HTTP requests for server.shutdown_timeout. The complete sequence,
including redrives and cleanup, is bounded by server.shutdown_total_timeout.

metrics.addr gives /metrics its own listener. A replay block adds the control
listener the replay service retries through. Failing to bind any of them
exits 1. Logs are JSON on stdout.

Probes: GET /healthz is liveness; GET /readyz also checks every destination
the flows publish to.`,
			Examples: []string{
				"hookway --config examples/config.yaml",
				"HOOKWAY_CONFIG=/etc/hookway/config.yaml HOOKWAY_LOG_LEVEL=debug hookway",
			},
		},
		{
			Name:    "validate",
			Args:    "[--config FILE] [--json] [--no-env] [--strict]",
			Summary: "check a config without starting anything",
			Help: `Checks YAML syntax, the schema, and every rule serve enforces, without
connecting to any destination. Lint warnings (a flow with no rate_limit, a
no_auth node, ...) are printed but pass unless --strict is set.

${VAR} references are expanded from the environment, and an unset variable
fails. With --json an unset variable is a warning instead, so CI can validate
without production secrets. --no-env skips expansion entirely.

Prints "config valid: FILE", or the issues on stderr. --json prints
{"issues": [...]} on stdout instead. Exits 0 when valid, 1 when not.`,
			Examples: []string{
				"hookway validate --config examples/config.yaml",
				"hookway validate --config config.yaml --strict           # lint warnings fail too",
				"hookway validate --config config.yaml --json --no-env    # for CI, no secrets needed",
			},
		},
		{
			Name:    "schema",
			Args:    "[--nodes | --destinations | --connections]",
			Summary: "print the config JSON Schema",
			Help: `Prints the JSON Schema for the config file. It is generated from the code,
so it cannot drift. Point your editor at it for completion and inline errors:

  # yaml-language-server: $schema=./hookway.schema.json

--nodes, --destinations, and --connections print the per-type catalogs the
config builder uses instead: each node's ports and options, each destination
type's options, each connection type's options. Pass one; if several are set,
the first in that order wins.`,
			Examples: []string{
				"hookway schema > hookway.schema.json",
				"hookway schema --destinations > destinations.json",
			},
		},
		{
			Name:    "editor",
			Args:    "[--addr ADDR] [--config FILE]",
			Summary: "serve the config builder and config API",
			Help: `Serves the config builder at /builder and the project overview at /, and the
API behind them: GET /schema, GET /node-schema, and POST /validate. POST
/validate answers like validate --json --no-env: it never expands ${VAR},
because the posted YAML is untrusted. The builder writes YAML; it does not
connect to a running Hookway.

With a config (--config or HOOKWAY_CONFIG) that sets security.api_key, the API
requires Authorization: Bearer <key>; without one it is open. The UI is only in
builds that ran make ui; otherwise / explains how to build it.`,
			Examples: []string{
				"hookway editor                                  # http://localhost:8090/builder",
				"hookway editor --addr :9000 --config config.yaml",
			},
		},
		{
			Name:    "healthcheck",
			Args:    "[--url URL] [--timeout DURATION]",
			Summary: "probe a running server",
			Help: `GETs the URL and exits 0 on any 2xx, 1 on anything else or no answer. The
container image runs it as its HEALTHCHECK. /healthz is liveness only; use
/readyz to include the destinations.`,
			Examples: []string{
				"hookway healthcheck",
				"hookway healthcheck --url http://127.0.0.1:8080/readyz --timeout 2s",
			},
		},
		{
			Name:    "version",
			Summary: "print the version and commit",
			Help:    `Prints "hookway <version> (<commit>)". A plain go build reports "dev (none)".`,
		},
	},
	Footer: `Start here:
  hookway validate --config examples/config.yaml
  hookway serve --config examples/config.yaml

Deploy this binary to receive requests and execute configured flows. Routing,
authentication, transforms, proxies and broker publication run inside it.
Use validate, schema and editor while preparing the config; healthcheck is
for container probes. Queue consumers are separate from Hookway.

Environment:
  HOOKWAY_CONFIG     the config file when --config is not given
  HOOKWAY_LOG_LEVEL  overrides logging.level at start: debug, info, warn, error

Exit codes: 0 success, 1 failure, 2 bad command line.
Docs: docs/reference/cli.md (commands), docs/reference/configuration.md (the config file).`,
}
