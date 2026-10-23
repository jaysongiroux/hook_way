# Create named events

Use a named event to give application payloads a shared contract. A normal flow
verifies a webhook, filters and transforms its data, creates an event, then
publishes it to an existing destination. Hookway does not run an AI model.

## Define a payload contract

The optional top-level `events` map is shared by all flows in a configuration:

```yaml
events:
  issue.opened:
    description: A new issue was opened.
    payload_schema:
      type: object
      required: [issue_id, title]
      properties:
        issue_id: {type: string}
        title: {type: string}
```

Names use Hookway's named-reference rules, including dotted names. Each entry
requires a nonempty description and a schema declaring `type: object`. Schemas
compile offline during validation and startup; references must be local to the
schema. Each schema is limited to 64 KiB and existing schema structure budgets.
Schemas preserve numeric constraints exactly in native YAML validation.

The catalog belongs in the entry YAML when using one file per flow. Unknown
fields, invalid schemas and unresolved event references fail validation. Existing
configurations need no event catalog.

## Create and deliver an event

After an envelope and any body transformations, add:

```yaml
- id: issue_event
  type: event
  event: issue.opened
  event_id_path: issue_id
- id: send
  type: publish
  destination: broker
  target: issue-events
  wire_body: json
```

Connect these nodes with `issue_event>send`. The event node validates the current
JSON object against `payload_schema`, without converting values, inserting
defaults or removing extra fields. Use existing Code or Transform nodes before
Create event to project source fields into the intended payload.

The resulting body contains `eventId`, `name`, `timestamp` and `data`. `data` is
the validated object; `timestamp` is construction time in UTC RFC3339 format.
The optional `event_id_path` is a dotted path in the current body and must resolve
to a nonempty string. Omitting it generates a fresh UUID. Use an explicit sender
ID when consumers need stable identity across sender redelivery; generated IDs
identify individual event constructions. Neither choice performs deduplication.

Event construction preserves its input and performs no delivery side effect. It
requires a success continuation. Invalid bodies, invalid ID selections and
resource-limit failures return a 400 node error, or follow `on_error` with the
original input. The complete occurrence is bounded to 1 MiB and existing JSON
structure limits. Validation applies at this node: later body transformations
can change the validated result.

`wire_body: json` delivers the current body for SQS/SNS and non-proxy webhook
destinations. Webhook retries remain configured on the destination; per-node
broker retry settings still require SQS/SNS. Other destinations keep their
existing payload modes and may carry the event inside Hookway's outer envelope.
Default envelope and proxy behavior is unchanged. Publish retries and retained
destination-redrive snapshots preserve the constructed event ID and timestamp.

## Author in the builder

Open **Config → Events** to add, edit, rename or remove definitions. Add
**Create event** from the node palette, select its event and optionally enter an
ID path. **Manage events** opens the shared definitions from the inspector.

Renaming a definition does not rewrite node references. Update affected nodes;
validation reports missing references. Invalid schema drafts remain available
after closing or reloading the editor, and exports use the last valid values
until the draft is corrected or reverted. Schemas that cannot be represented
exactly by JavaScript numbers cannot replace a builder workspace; keep those
configurations in YAML. Schema keys and references stay literal, and builder
exports preserve dollar signs in schema values.

## Run the example

The complete [named-events example](../../examples/adapters/named-events.yaml)
receives authenticated requests at `/webhooks/issues`, keeps opened issues,
projects their fields and sends JSON to `/issue-events` on your receiver.
Set `SAMPLE_WEBHOOK_SECRET` to a secret of your choosing and `WEBHOOK_URL` to your
receiver's base URL, then run:

```sh
go run ./cmd/hookway validate --config examples/adapters/named-events.yaml --strict
HOOKWAY_CONFIG=examples/adapters/named-events.yaml go run ./cmd/hookway
```

From another terminal with the same secret available:

```sh
curl --fail-with-body http://localhost:8080/webhooks/issues \
  -H 'Content-Type: application/json' \
  -H "X-Api-Key: $SAMPLE_WEBHOOK_SECRET" \
  --data '{"action":"opened","issue":{"id":"issue-1","title":"Example issue"}}'
```

A valid request receives 202 after synchronous destination delivery succeeds.
An opened issue with a missing or invalid title receives 400 and is not published.
Other actions are filtered out without delivery. Destination acceptance is not
exactly-once business processing; see [delivery guarantees](../operations/delivery-guarantees.md).

Create event produces transport-independent event payloads. Optional
[MCP event delivery](mcp-events.md) adds durable subscriptions. By itself, event
construction does not advertise
MCP Events capabilities or implement discovery, subscriptions, subscriber access
rules, cursors, retained event storage or agent wake-up.
