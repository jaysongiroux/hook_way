# LaunchDarkly feature flags

The `launchdarkly` node evaluates an explicit list of feature flags for a user or tenant. Use it for tenant rollouts, processor migrations, routing choices and operational switches. It accepts and returns an envelope, after authentication and parsing. Flags remain separate from the webhook body.

## Configure a connection

```yaml
launchdarkly:
  production:
    sdk_key_env: LAUNCHDARKLY_SDK_KEY
    initialization_timeout: 5s
    send_events: true
```

Set `LAUNCHDARKLY_SDK_KEY` on the server to the **server SDK key for the intended LaunchDarkly project and environment**. The configuration contains only its environment variable name, never the key. Missing or empty environment variables prevent startup. CLI and browser validation check structure without reading the variable or contacting LaunchDarkly.

The server shares one Go SDK client per connection across flows. It evaluates flags from local SDK data; it does not call the REST management API per webhook or maintain a separate per-context result cache. Initialization waits up to `5s` by default. A network timeout permits fallback processing while the same client reconnects. Set a positive timeout of at most `1m`. Rotate keys by updating the environment and restarting the server.

## Evaluate named flags

```yaml
- id: evaluate
  type: launchdarkly
  connection: production
  name: rollout
  context:
    kind: organization
    key: {source: body, path: tenant.id}
    attributes:
      plan: {source: body, path: tenant.plan}
      region: {source: vars, path: region}
      cohort: {value: early_access}
  flags:
    new_processor: {key: use-new-processor, type: boolean, fallback: false}
    processor: {key: processor-name, type: string, fallback: legacy, variable: processor}
    limit: {key: rate-limit, type: number, fallback: 100}
    settings: {key: tenant.settings, type: json, fallback: null}
```

The result namespace must be unique in its flow. Names and aliases use letters, digits and underscores, begin with a letter or underscore, and have at most 64 characters. The LaunchDarkly flag key is separate and may contain dots. Each flag needs an explicit fallback matching its declared type. `false`, zero, empty strings and JSON `null` are meaningful values, not missing defaults.

One context is supported. Its kind defaults to `user`; choose the actual kind configured in LaunchDarkly, such as `organization`. Its key must resolve to a nonempty string. A stable tenant key keeps tenant-level targeting and percentage rollouts consistent. Selectors choose either a literal `value` or a `source` and `path`: `body` reads dotted JSON object paths, `header` reads request headers case-insensitively, and `vars` reads an existing variable. Attributes retain their JSON types. Missing attributes, null attributes and invalid keys make the context invalid and use all configured fallbacks. Select only needed attributes.

## Route and bind results

Use `source: flags` in `filter`, `if_else` or `switch`:

```yaml
conditions:
  - {source: flags, path: rollout.new_processor.value, operator: eq, value: true}
  - {source: flags, path: rollout.settings.value.limit, operator: gte, value: 10}
```

Each result contains `value`, `used_fallback`, `reason` and, on errors, a sanitized `error_kind`. Check `rollout.new_processor.used_fallback` to distinguish a fallback from a legitimate disabled flag. A flag's ordinary off variation is a successful evaluation. Missing flags, wrong types and unavailable SDK state use the configured fallback and continue. SDK updates may affect different named evaluations during one event; the list is not an atomic snapshot of LaunchDarkly state.

Existing scalar operators apply. Read nested JSON object fields using dotted paths; array indexing and whole-object comparisons are not supported. Null counts as present for `exists`.

An optional `variable` makes a result available to existing `{variable}` templates. Strings stay plain; boolean and numeric values become text; JSON values become compact JSON, including `null`. Bindings follow lookup assignment behavior and may explicitly overwrite an existing variable. The usual downstream target/header validation still applies. Flags are not inserted into the body. Explicit variable bindings can appear in the published envelope's variable metadata.

## Privacy and observability

SDK evaluation events are enabled by default. Custom attributes are private: they can affect local targeting without being sent as attribute values. **Context key and kind still reach LaunchDarkly.** Set `send_events: false` to disable SDK analytics events. The browser builder never runs the SDK.

`hookway_flag_evaluations_total{node,result}` distinguishes `evaluated` and `fallback`. `hookway_flag_connection_status{connection,state}` exposes the data source's `INITIALIZING`, `VALID`, `INTERRUPTED` or `OFF` state. Labels use configured names and finite states, not event context identifiers, flag values or credentials. SDK diagnostic/evaluation logs are disabled; use these metrics to inspect connection failures. This integration does not gate readiness on the SDK connection because serving with fallbacks is intentional.

A disconnected client may keep using its last known flag data. A remote change, including a kill switch, is not guaranteed to reach it until connectivity recovers. A fresh disconnected instance has no such data and uses fallbacks.

## Replay compatibility

New delivery snapshots use format **v2** and retain typed flag results on resume. A retry at a downstream node uses the original decisions, even if the live flags changed. Publish-only replay sends the already captured message without evaluating flags again. If execution actually resumes at the LaunchDarkly node, it evaluates the context against the current SDK state.

This release reads existing v1 captures as having no flag metadata. Upgrade core and replay together before enabling new captures: older binaries cannot decode v2 snapshots, even for flows without LaunchDarkly. No database migration is required.

## Try the example

[The runnable tenant-rollout example](../../examples/launchdarkly.yaml) demonstrates all four flag types and branches to different responses.

```sh
export LAUNCHDARKLY_SDK_KEY='<your server SDK key>'
go run ./cmd/hookway serve --config examples/launchdarkly.yaml
```

In another terminal:

```sh
curl -X POST http://localhost:8080/webhooks/rollout \
  -H 'Content-Type: application/json' \
  -d '{"tenant":{"id":"acme","plan":"pro"}}'
```

Create the listed flags in that environment and target the `organization` context key `acme`. Without those flags, the example follows the false fallback and responds with `existing processor: legacy`. It uses `no_auth` for local demonstration; use an authenticated flow in production.

The initial version supports ordinary envelope-stage nodes, single contexts and SDK default service endpoints. Batch-child placement, multi-context targeting, Relay Proxy overrides and flag-management operations are not supported.
