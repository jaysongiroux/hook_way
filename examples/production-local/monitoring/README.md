# Optional local monitoring

Start the production-local application with its normal `make prod-local-*`
commands first. Monitoring is optional, uses private authenticated metric
listeners, and requires no cloud account or notification credentials.
Prometheus and Alertmanager UIs publish only on loopback; this is not an
internet-facing monitoring deployment or a route to a real responder.

Prepare private files from the bootstrap-generated trusted `.env`:

```sh
export HOOKWAY_LOCAL_DIRECTORY="$(pwd)/examples/production-local/.local"
export HOOKWAY_MONITOR_UID="$(id -u)" HOOKWAY_MONITOR_GID="$(id -g)"
umask 077
mkdir -p "$HOOKWAY_LOCAL_DIRECTORY/monitoring"
chmod 700 "$HOOKWAY_LOCAL_DIRECTORY/monitoring"
set -a
. "$HOOKWAY_LOCAL_DIRECTORY/.env"
set +a
printf '%s' "$HOOKWAY_API_KEY" > "$HOOKWAY_LOCAL_DIRECTORY/monitoring/core-token"
printf '%s' "$REPLAY_METRICS_TOKEN" > "$HOOKWAY_LOCAL_DIRECTORY/monitoring/replay-token"
chmod 600 "$HOOKWAY_LOCAL_DIRECTORY/monitoring/core-token" "$HOOKWAY_LOCAL_DIRECTORY/monitoring/replay-token"
docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  --project-name "$COMPOSE_PROJECT_NAME" \
  -f examples/production-local/docker-compose.yaml \
  -f examples/production-local/monitoring/compose.yaml \
  --profile monitoring up -d --no-deps prometheus alertmanager alert-receiver
```

Use the same `--project-name` as the application (`PROD_LOCAL_PROJECT` if
overridden). Token files stay mode 600 and the scraper runs as their owner;
do not make secrets world-readable. Refresh files after credential rotation.
Run this recipe as a non-root local operator; do not set the monitoring UID to 0.
Docker volume paths resolve against the first Compose file, not this directory.
Inspect [Prometheus](http://localhost:19090/) targets/rules and
[Alertmanager](http://localhost:19093/). The echo receiver acknowledges local
webhooks but is not a retained notification ledger; the maintained acceptance
test independently verifies full firing/resolved webhook bodies.

For core-only mode copy `prometheus.yaml` into the ignored local directory,
remove the replay scrape job and override its config mount. Do not start replay
solely for monitoring or bypass metrics authentication. One replay job must
contain replicas of one logical database; separate databases need separate jobs.
Shared database gauges use `max by (job, ...)`, not sums across replicas.
GC/freshness/database accessibility remain replica-local diagnostics.

These sample thresholds are starting points, not agreed SLOs. Review alert hold
durations, GC cadence, storage limits and incident ownership in the
[observability runbook](../../../docs/operations/observability.md). Load the
[existing dashboards](../../../metrics/grafana/) into an operator-managed
Grafana if desired; Grafana is not required for application operation.

Monitoring data/silences use bounded temporary storage and are lost on container
recreation. Application volumes are unchanged. To remove only monitoring, use
the same Compose arguments with `stop prometheus alertmanager alert-receiver`,
then `rm -f prometheus alertmanager alert-receiver`. Do not use `down -v`.

Checks: `make observability-check` validates rules/dashboards and runs the real
Prometheus-to-Alertmanager notification regression when Docker is available;
set `HOOKWAY_REQUIRE_INTEGRATION=1` to require it. The test accelerates windows
and hold durations in a fixture copy. It separates actual runtime auth/admission
faults from synthetic database/GC rule inputs and never contacts a live receiver.
