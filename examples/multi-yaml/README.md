# One YAML per flow

`core.yaml` contains central settings and named flow references. Each file under
`flows/` contains exactly one flow body, using the same node and edge format as
an inline flow. No destinations or external dependencies are needed for this example.

From the repository root:

```sh
go run ./cmd/hookway validate --config examples/multi-yaml/core.yaml --strict
go run ./cmd/hookway serve --config examples/multi-yaml/core.yaml
```

In another terminal:

```sh
curl -i -X POST http://localhost:8080/orders \
  -H 'X-Auth: change-me-demo-token' \
  -H 'Content-Type: application/json' -d '{"id":1}'
curl -i -X POST http://localhost:8080/billing \
  -H 'X-Auth: change-me-demo-token' \
  -H 'Content-Type: application/json' -d '{"id":2}'
```

Each endpoint responds with HTTP 202 and its flow's acknowledgement. The token
is a demonstration value; replace it with a quoted environment reference for
real deployments, and set that variable before starting Hookway.

Deploy or mount the complete directory and point `--config` at its `core.yaml`.
Flow file paths resolve relative to that entry file rather than the process's
working directory. Changes take effect after restart.

The builder can import this directory or its ZIP through **Config → Import
YAML → Files / ZIP**. **Export YAML → YAML organization** converts between the
inline and per-flow layouts. Both multi-flow layouts download as a ZIP, with
`replay.yaml` included when replay has been configured.
