import { nodeDescriptor } from "./nodeDescriptors";
import { NODE_TYPES } from "./nodeSchema";

export const PALETTE_GROUP_HELP: Record<string, string> = {
  Templates:
    "Add a ready-made webhook flow, or a Slack or Discord notification branch.",
  "Receive requests": "Choose the URL that starts this flow.",
  "Check requests": "Check the client and set limits before reading the body.",
  "Verify the sender": "Check authentication using the original request.",
  "Prepare the event":
    "Parse an authenticated request or check for duplicates.",
  "Process the event": "Filter, branch, transform, or call another service.",
  "Deliver and respond": "Send events or return a response to the caller.",
};

const COMMON_FIELD_HELP: Record<string, string> = {
  secret: "Literal secret or ${ENV_VAR}. Prefer secret_ref in production.",
  secret_ref:
    "Resolve the secret from env, a file, or AWS Secrets Manager, with optional refresh.",
  provider: "Where the value is read from.",
  name: "Identifier the provider uses (env var, file path, or secret name).",
  refresh:
    "How often to re-read, e.g. 1m. Empty defaults to a refresh every 15 minutes.",
  overlap: "How long the previous secret still works during rotation, e.g. 5m.",
  source: "Where the value is taken from.",
  path: "Path or key used to read the value from its selected source.",
  operator: "Comparison to apply (eq, contains, …).",
  value: "Configured value for this field.",
  port: "Label on the outgoing handle this case uses.",
  when: "Condition that selects this port.",
  kind: "Which mechanism to use.",
  key: "Name of the header, query param, or lookup key.",
  ttl: "How long to keep the cached value, e.g. 30s.",
  max_entries: "In-memory cap for this process.",
  cache: "Optional in-process cache for lookup results.",
  url: "HTTP(S) URL.",
  timeout: "Duration string such as 5s or 1m.",
  // Nested help asks for the qualified path before trying the bare field name.
  "json.max_depth":
    "Parsed JSON depth, with the root at depth 0. Default 128; choose 1–1024. Exceeding it returns 413. The read-body byte limit is separate.",
  "json.max_nodes":
    "JSON values allowed before returning 413. Default 1000000; choose 1–1000000. Objects, arrays and scalars count, including values under repeated object keys; keys do not.",
  "retry.max_attempts":
    "Total attempts including the first. 1 (or empty) means no retry. Max 10.",
  "multipart.max_parts":
    "Parts allowed before the request is rejected with 413. Default 16, max 256.",
  "multipart.max_part_bytes":
    "Per-part size cap in bytes. Default 1048576 (1 MiB).",
  "multipart.include_content":
    "Carry each file's bytes as base64. Turn off to keep file metadata only without retaining a base64 content copy. Parsing still uses a bounded temporary part buffer.",
  "retry.initial_backoff":
    "Wait before the second HTTP request attempt; waits double with jitter. Defaults to 500ms.",
  "retry.max_backoff":
    "Maximum wait between HTTP request attempts, including a 429 Retry-After. Defaults to 30s.",
  "conditions.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
};

export function nodeHelp(type: string): string {
  return nodeDescriptor(type)?.help ?? "";
}

const PORT_LABELS: Record<string, string> = {
  None: "start",
  RequestHead: "request headers",
  RawRequest: "original request bytes",
  Verified: "authenticated request",
  Envelope: "parsed event",
  Terminal: "flow ends",
};

export function portLabel(type: string): string {
  return PORT_LABELS[type] ?? type;
}

/** Display labels only; YAML keys and generated runtime schemas stay intact. */
export function nodeFieldLabels(type: string): Record<string, string> {
  return {
    name: "Result variable",
    timeout: "Timeout",
    secret_ref: "Secret source",
    max_entries: "Maximum cached keys",
    ...nodeDescriptor(type)?.fieldLabels,
  };
}

export function nodeFieldHelp(
  type: string | undefined,
  field: string,
): string | undefined {
  if (type) {
    const specific = nodeDescriptor(type)?.fieldHelp?.[field];
    if (specific) return specific;
  }
  if (COMMON_FIELD_HELP[field]) return COMMON_FIELD_HELP[field];
  const leaf = field.includes(".")
    ? field.slice(field.lastIndexOf(".") + 1)
    : field;
  if (leaf !== field && COMMON_FIELD_HELP[leaf]) return COMMON_FIELD_HELP[leaf];
  if (type) {
    const specificLeaf = nodeDescriptor(type)?.fieldHelp?.[leaf];
    if (specificLeaf) return specificLeaf;
  }
  return undefined;
}

export const SERVER_FIELD_HELP: Record<string, string> = {
  addr: "Listen address for webhook traffic, e.g. :8080.",
  max_body_bytes:
    "Reference limit for validation warnings (bytes). Set the enforced limit on each Read request body node.",
  read_timeout: "How long to wait to read the request, e.g. 5s.",
  write_timeout: "How long to wait to write the response, e.g. 10s.",
  idle_timeout: "Keep-alive idle time on the HTTP listener, e.g. 60s.",
  shutdown_delay:
    "Time to keep serving after readiness turns unhealthy, so the load balancer can remove this replica. Omitted uses 0s; set this to your traffic withdrawal time.",
  shutdown_timeout:
    "HTTP request drain budget after traffic withdrawal, e.g. 10s. Must cover the publish timeout.",
  shutdown_total_timeout:
    "Total time allowed for withdrawal, request and replay drain, and resource cleanup. Omitted uses the greater of 60s or withdrawal plus request drain plus 30s. Allow extra time for replay and publisher drains; set the platform termination allowance longer than this limit.",
  publish_timeout:
    "Deadline for delivering after the request is accepted, e.g. 10s.",
  max_inflight_requests:
    "Concurrent webhooks before shedding with 503. 0 uses the default; -1 disables the cap.",
  fanout_concurrency:
    "Concurrent publishes per request when delivery_failure_policy is skip_failed. 0 uses the default of 8. Also sizes the async webhook delivery worker pool.",
  readiness_interval: "How often destinations are probed for /ready, e.g. 10s.",
  readiness_timeout: "Per-probe deadline, e.g. 5s.",
  readiness_require_all:
    "When true, /ready is 503 if any flow is unready. Default: 200 while at least one flow is ready.",
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
  invocation_type:
    "Lambda: Event (default, fire-and-forget) or RequestResponse (wait for the function).",
  bucket: "S3 bucket name.",
  table: "Database table to insert into.",
  database: "MongoDB database name.",
  host_port: "Temporal frontend address, host:port.",
  namespace: "Temporal namespace.",
  timeout: "HTTP timeout for this destination.",
  delivery_mode:
    "async (default) acknowledges an in-memory job before HTTP delivery. sync waits for the destination response within the publication timeout.",
  max_queue:
    "Capacity of the in-memory async delivery queue. A process crash can lose queued work.",
  max_queue_bytes:
    "Accounted byte budget for queued and active async deliveries, including encoding and metadata headroom. Omitted or 0 uses 67108864 (64 MiB); set a positive limit to override. Sync delivery is unaffected. This is not a process memory limit.",
  drain_timeout:
    "Time allowed to finish queued async deliveries during graceful shutdown; also bounded by the server's total shutdown limit.",
  mode: "Redis topology: single, sentinel, or cluster.",
  key_prefix: "Prefix applied to Redis keys.",
};

export function destFieldHelp(field: string): string | undefined {
  return DEST_FIELD_HELP[field] ?? COMMON_FIELD_HELP[field];
}

export function missingNodeHelp(): string[] {
  return NODE_TYPES.filter((t) => !nodeDescriptor(t)?.help);
}
