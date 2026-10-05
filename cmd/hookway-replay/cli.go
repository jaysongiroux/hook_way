package main

import "github.com/jaysongiroux/hook_way/internal/cli"

// program is hookway-replay's help: `hookway-replay help`,
// `hookway-replay help <command>`, and `hookway-replay <command> -h`.
// docs/reference/cli.md is the long form; a test keeps the two listing the same
// commands and flags.
var program = cli.Program{
	Name:    "hookway-replay",
	Summary: "store failed ingress deliveries in PostgreSQL and retry them through core",
	Commands: []cli.Command{
		{
			Name:    "serve",
			Args:    "[--config FILE]",
			Summary: "run the replay API, retry workers, and retention sweep",
			Help: `Serves the replay API on server.addr (and /metrics on metrics.addr when set),
claims and runs queued retries against core's control listener, and deletes
expired records every retention.gc_interval, until SIGINT or SIGTERM. Shutdown
drains the API and waits for running retries to finish.

API: GET /v1/failures, GET /v1/failures/{id}, GET /v1/failures/{id}/payload,
POST /v1/failures/{id}/retries, POST /v1/failures/{id}/dismiss,
POST /v1/retries (batch), GET /v1/retries/{run_id}. Each needs a key with the
matching scope; see docs/operations/replay-service.md.

Refuses to start when the database schema is behind this binary: run migrate
first. Any number of replicas can share one database. Logs are JSON on stdout.`,
			Examples: []string{
				"hookway-replay serve --config /etc/hookway-replay/replay.yaml",
				"HOOKWAY_REPLAY_CONFIG=replay.yaml hookway-replay serve",
			},
		},
		{
			Name:    "migrate",
			Args:    "[--config FILE]",
			Summary: "apply database migrations",
			Help: `Applies every pending schema migration and prints the schema version. Running
it again reports that the schema is current. Replicas may run it at the same
time: a PostgreSQL advisory lock serializes them. Run it before the first
serve and after every upgrade.`,
			Examples: []string{"hookway-replay migrate --config replay.yaml"},
		},
		{
			Name:    "gc",
			Args:    "[--config FILE]",
			Summary: "delete expired records once",
			Help: `Deletes the records past retention.ttl, prints how many and how many bytes it
freed, and exits. serve already sweeps every retention.gc_interval; run gc
from cron, or right after shortening the retention. When another replica is
sweeping, it says so and exits 0.`,
			Examples: []string{"hookway-replay gc --config replay.yaml"},
		},
		{
			Name:    "validate",
			Args:    "[--config FILE]",
			Summary: "check a config without connecting to anything",
			Help: `Checks the config without connecting to the database or to core. ${VAR}
references are expanded from the environment, and an unset variable fails.
Warnings are printed but pass. Exits 0 when valid, 1 when not.`,
			Examples: []string{"hookway-replay validate --config examples/dev/replay.yaml"},
		},
		{
			Name:    "healthcheck",
			Args:    "[--url URL] [--timeout DURATION]",
			Summary: "probe a running service",
			Help: `GETs the URL and exits 0 on any 2xx, 1 on anything else or no answer. The
container image runs it as its HEALTHCHECK. /healthz is liveness only; /readyz
also checks the database and its schema version.`,
			Examples: []string{
				"hookway-replay healthcheck",
				"hookway-replay healthcheck --url http://127.0.0.1:8080/readyz",
			},
		},
		{
			Name:    "version",
			Summary: "print the version and commit",
			Help:    `Prints "hookway-replay <version> (<commit>)". A plain go build reports "dev (none)".`,
		},
	},
	Footer: `Optional service: deploy alongside core only when you need to inspect and
retry failed deliveries after an early acknowledgement. It requires a replay
config, PostgreSQL, and core's replay capture/control configuration.

First start: run migrate --config replay.yaml, then serve --config replay.yaml.
Retries repeat the failed destination publication through core. This service
does not consume messages from SQS or other destination queues.

Environment:
  HOOKWAY_REPLAY_CONFIG  the config file when --config is not given
  HOOKWAY_LOG_LEVEL      overrides logging.level at start: debug, info, warn, error

Exit codes: 0 success, 1 failure, 2 bad command line.
Docs: docs/reference/cli.md (commands), docs/operations/replay-service.md (API, config, operations).`,
}
