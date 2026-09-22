import { NODE_TYPES } from "./nodeSchema";

/** One sentence per catalog node — palette, inspector, and empty-canvas teaching. */
const NODE_HELP: Record<string, string> = {
  http_trigger: "HTTP entry point: the public path and methods that start this flow.",
  loop_guard: "Rejects a request that has already passed through Hookway too many times.",
  ip_allowlist: "Allows the request only when the client IP is in one of the listed CIDRs.",
  rate_limit: "Caps how many requests this process will accept per minute (per pod).",
  read_body: "Buffers the raw request body so later nodes can verify or parse it.",
  auth: "Verifies the webhook signature or token against the raw bytes.",
  no_auth: "Marks the request verified without checking a signature — use only for trusted callers.",
  replay_guard: "Drops a request whose event key was already seen within the TTL.",
  envelope: "Decodes the verified body into the event object later nodes work with.",
  filter: "Continues only when the event matches; otherwise drop or reject.",
  lookup: "Embedding-only: loads extra data when a provider is injected. Standalone CLI/serve reject it.",
  transform: "Rewrites the event (field map, changed-fields diff, or XML prune).",
  code: "Runs a short sandboxed JavaScript snippet against a copy of body, vars, and headers.",
  switch: "Sends the event out exactly one labelled port, based on the first matching case.",
  if_else:
    "Sends the event out the true port when the conditions hold, and the false port when they do not. Both ports must be wired.",
  http_request: "Calls an upstream HTTP API and can return that response to the sender.",
  publish: "Delivers the event to a named destination. An optional single success edge runs after delivery with the same envelope, sharing server.publish_timeout; respond cannot follow.",
  respond: "Ends the flow with an HTTP response to the original sender.",
};

export const PALETTE_GROUP_HELP: Record<string, string> = {
  Templates: "Ingest starters verify a signed webhook with loop, rate, and replay guards. Notification templates format an event and POST it without forwarding caller credentials.",
  Trigger: "Where the HTTP request enters.",
  "Request head": "Look at method, path, and headers before reading the body.",
  "Raw body": "Verify the payload bytes.",
  Verified: "After authentication succeeded.",
  Envelope: "Work with the decoded event.",
  Terminal: "Publish somewhere or answer the sender.",
};

/** Field copy keyed by "nodeType.field" then by bare field name. */
const NODE_FIELD_HELP: Record<string, string> = {
  "http_trigger.path": "Public URL path, for example /webhooks/github. May include {params}.",
  "http_trigger.methods": "HTTP methods this trigger accepts. POST is the usual webhook default.",
  "loop_guard.max_hops": "How many Hookway hops are allowed before the request is rejected as a loop.",
  "ip_allowlist.cidrs": "CIDR ranges that may call this flow, e.g. 10.0.0.0/8.",
  "rate_limit.rpm": "Allowed requests per minute in this process. N pods means N times this limit.",
  "rate_limit.burst": "Extra requests allowed in a short burst above rpm. 0 means no extra burst.",
  "rate_limit.key_by": "What to count against: empty is the whole flow; header:Name or ip are typical.",
  "read_body.max_bytes": "Largest body to buffer. Larger requests are rejected before auth.",
  "auth.scheme": "How the signature is laid out in the header (digest, encoding, timestamp).",
  "auth.secret": "Shared secret, often ${WEBHOOK_SECRET}. Use secret_ref instead of a literal in production.",
  "auth.signature_header": "Request header that carries the signature, e.g. X-Hub-Signature-256.",
  "auth.timestamp_tolerance": "How old a signed timestamp may be before auth fails, e.g. 5m.",
  "auth.remove_bearer": "Strip a leading Bearer prefix from the token before verifying.",
  "auth.expected_issuer": "JWT iss claim that must match. Leave empty to skip.",
  "auth.expected_audience": "JWT aud claim that must match. Leave empty to skip.",
  "auth.body_hash_claim": "JWT claim that must hash-match the raw body.",
  "replay_guard.ttl": "How long a seen event key is remembered, e.g. 24h.",
  "replay_guard.max_entries": "In-memory cap for remembered keys in this process.",
  "replay_guard.key": "Event identity to remember. Empty uses the default id from the payload.",
  "envelope.body_format":
    "How to decode the body: json objects, xml element tree, multipart form-data parts, or raw bytes (raw leaves the body empty).",
  "envelope.multipart":
    "Limits for body_format: multipart. Bounds how much of an upload is held in memory.",
  "filter.conditions": "All conditions must match for the event to continue.",
  "filter.on_no_match": "drop acknowledges 202 without publishing; reject fails the request.",
  "lookup.provider": "Where to read the extra data from (env, file, dynamodb, …).",
  "lookup.key": "Lookup key, with ${vars} allowed.",
  "lookup.name": "Variable name to store the result under.",
  "lookup.table": "Table or collection when the provider needs one.",
  "lookup.fallback": "Value to use when the lookup misses.",
  "lookup.negative_ttl": "How long to remember a miss so it is not re-fetched immediately.",
  "transform.kind": "changed_fields and field_map need a json body; xml_prune needs an xml one. Validation rejects a mismatch.",
  "transform.from": "Source path for changed_fields.",
  "transform.to": "Destination path for changed_fields.",
  "transform.name": "Variable name that receives the transform result.",
  "transform.mappings": "from/to path pairs for field_map.",
  "transform.rules": "Named XML prune rules to apply.",
  "code.script": "JavaScript. Input is a copy of {body, vars, headers}. Return {body, vars} only.",
  "code.timeout": "How long the script may run, e.g. 50ms.",
  "code.max_input_bytes": "Hard cap on the serialized input passed to the script.",
  "code.max_output_bytes": "Hard cap on what the script may return.",
  "code.max_call_stack": "Call-stack depth the sandbox will allow.",
  "switch.cases": "Each case is a labelled output port. The first match wins.",
  "switch.cases.match": "all requires every condition in the case; any requires one. Defaults to all.",
  "switch.cases.conditions": "Conditions for this case, joined by match. Use this instead of when, not alongside it.",
  "switch.cases.when": "A single condition for this case. The conditions list is the multi-condition form.",
  "switch.default": "Port used when no case matches. Must match a case port or a dedicated default handle.",
  "if_else.match": "all requires every condition to hold; any requires one. Defaults to all.",
  "if_else.conditions": "Conditions joined by match. They decide the true port from the false port.",
  "http_request.url": "Upstream URL. ${vars} are expanded.",
  "http_request.forward_method": "Send the original request method instead of POST.",
  "http_request.forward_query": "Copy the original query string onto the upstream URL.",
  "http_request.forward_headers": "Request header names to copy upstream.",
  "http_request.timeout": "Upstream deadline, e.g. 10s.",
  "http_request.headers": "Extra headers to send. Values may use ${vars}.",
  "http_request.error_on_status": "Status classes that fail this node, e.g. 5xx.",
  "http_request.body": "raw sends the original bytes; transformed sends the current event body.",
  "http_request.retry": "Optional: re-attempt transport errors, timeouts, 429, and 5xx. Leave empty for a single attempt.",
  "publish.destination": "Id of an entry in Destinations.",
  "publish.target": "Queue, topic, routing key, webhook path, or Lambda function name/ARN on that destination.",
  "publish.create": "Declare the queue/topic at startup when the backend supports it (not Lambda).",
  "publish.message_attributes": "SNS/SQS message attributes (string, number, or String.Array).",
  "publish.payload": "envelope (default JSON Message) or object (raw file bytes; s3 and redis_kv only).",
  "respond.status": "HTTP status to return. Omit if passthrough is on.",
  "respond.headers": "Response headers to add.",
  "respond.body": "Response body. Ignored when passthrough is on.",
  "respond.passthrough": "Return the last upstream http_request response instead of a fixed status/body.",
};

const COMMON_FIELD_HELP: Record<string, string> = {
  secret: "Literal secret or ${ENV_VAR}. Prefer secret_ref in production.",
  secret_ref: "Resolve the secret from env, a file, or AWS Secrets Manager, with optional refresh.",
  provider: "Where the value is read from.",
  name: "Identifier the provider uses (env var, file path, or secret name).",
  refresh: "How often to re-read, e.g. 1m. Empty means once at startup.",
  overlap: "How long the previous secret still works during rotation, e.g. 5m.",
  source: "Where the value is taken from (body, header, query, vars, …).",
  path: "Dot path inside that source, e.g. repository.full_name.",
  operator: "Comparison to apply (eq, contains, …).",
  value: "Right-hand side of the comparison.",
  port: "Label on the outgoing handle this case uses.",
  when: "Condition that selects this port.",
  kind: "Which mechanism to use.",
  key: "Name of the header, query param, or lookup key.",
  ttl: "How long to keep the cached value, e.g. 30s.",
  max_entries: "In-memory cap for this process.",
  cache: "Optional in-process cache for lookup results.",
  url: "HTTP(S) URL.",
  timeout: "Duration string such as 5s or 1m.",
  // Keyed "<parent>.<child>" because SchemaForm's nestedHelp asks for the
  // qualified name first, and nodeFieldHelp only consults this map (not
  // NODE_FIELD_HELP) for a dotted field.
  "retry.max_attempts": "Total attempts including the first. 1 (or empty) means no retry. Max 10.",
  "multipart.max_parts": "Parts allowed before the request is rejected with 413. Default 16, max 256.",
  "multipart.max_part_bytes": "Per-part size cap in bytes. Default 1048576 (1 MiB).",
  "multipart.include_content":
    "Carry each file's bytes as base64. Turn off to keep metadata only and copy nothing.",
  "retry.initial_backoff": "Wait before the second attempt, doubled with jitter each time. Defaults to 500ms.",
  "retry.max_backoff": "Ceiling for any single wait, including a 429's Retry-After. Defaults to 30s.",
  "conditions.path": "Header name, or a dotted body path: JSON keys (repository.private) or XML elements with @ for attributes (ORDER.@id).",
};

export function nodeHelp(type: string): string {
  return NODE_HELP[type] ?? "";
}

export function nodeFieldHelp(type: string | undefined, field: string): string | undefined {
  if (type) {
    const specific = NODE_FIELD_HELP[`${type}.${field}`];
    if (specific) return specific;
  }
  if (COMMON_FIELD_HELP[field]) return COMMON_FIELD_HELP[field];
  const leaf = field.includes(".") ? field.slice(field.lastIndexOf(".") + 1) : field;
  if (leaf !== field && COMMON_FIELD_HELP[leaf]) return COMMON_FIELD_HELP[leaf];
  if (type) {
    const specificLeaf = NODE_FIELD_HELP[`${type}.${leaf}`];
    if (specificLeaf) return specificLeaf;
  }
  return undefined;
}

export const SERVER_FIELD_HELP: Record<string, string> = {
  addr: "Listen address for webhook traffic, e.g. :8080.",
  max_body_bytes: "Largest request body the server will accept (bytes).",
  read_timeout: "How long to wait to read the request, e.g. 5s.",
  write_timeout: "How long to wait to write the response, e.g. 10s.",
  idle_timeout: "Keep-alive idle time on the HTTP listener, e.g. 60s.",
  shutdown_timeout: "How long SIGTERM waits for in-flight work, e.g. 10s.",
  publish_timeout: "Deadline for delivering after the request is accepted, e.g. 10s.",
  max_inflight_requests: "Concurrent webhooks before shedding with 503. 0 uses the default; -1 disables the cap.",
  fanout_concurrency: "Concurrent publishes per request when delivery_failure_policy is skip_failed. 0 uses the default of 8. Also sizes the async webhook delivery worker pool.",
  readiness_interval: "How often destinations are probed for /ready, e.g. 10s.",
  readiness_timeout: "Per-probe deadline, e.g. 5s.",
  readiness_require_all: "When true, /ready is 503 if any flow is unready. Default: 200 while at least one flow is ready.",
};

const DEST_FIELD_HELP: Record<string, string> = {
  url: "Connection or HTTP URL. ${ENV} references are expanded at load.",
  uri: "MongoDB connection URI.",
  brokers: "Kafka bootstrap brokers, comma-separated host:port.",
  exchange: "RabbitMQ exchange to publish through.",
  exchange_type: "Exchange kind: direct, topic, fanout, or headers.",
  durable: "Survive broker restart.",
  declare_queues: "Create missing queues at startup.",
  region: "AWS region, e.g. us-east-1.",
  endpoint: "Override the AWS endpoint (LocalStack, VPC endpoint).",
  invocation_type: "Lambda: Event (default, fire-and-forget) or RequestResponse (wait for the function).",
  bucket: "S3 bucket name.",
  table: "Database table to insert into.",
  database: "MongoDB database name.",
  host_port: "Temporal frontend address, host:port.",
  namespace: "Temporal namespace.",
  timeout: "HTTP timeout for this destination.",
  delivery_mode: "envelope sends the Hookway event; proxy forwards the original request.",
  mode: "Redis topology: single, sentinel, or cluster.",
  key_prefix: "Prefix applied to Redis keys.",
};

export function destFieldHelp(field: string): string | undefined {
  return DEST_FIELD_HELP[field] ?? COMMON_FIELD_HELP[field];
}

export function missingNodeHelp(): string[] {
  return NODE_TYPES.filter((t) => !NODE_HELP[t]);
}
