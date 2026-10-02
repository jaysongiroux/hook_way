import { NODE_TYPES } from "./nodeSchema";

/** One sentence per catalog node — palette, inspector, and empty-canvas teaching. */
const NODE_HELP: Record<string, string> = {
  http_trigger:
    "Start the flow when a request reaches the configured HTTP path.",
  loop_guard:
    "Reject requests that have passed through Hookway too many times.",
  ip_allowlist:
    "Accept requests only from the client IP addresses or networks you allow.",
  rate_limit:
    "Limit requests per minute for each client IP or header value, separately on each replica.",
  read_body:
    "Read the original request bytes and reject bodies above your size limit.",
  auth: "Check the sender’s signature, token, or HTTP Basic credentials before processing the request.",
  no_auth:
    "Accept requests without verifying the sender; use only when unauthenticated access is intended.",
  replay_guard:
    "Reject a repeated request while its key is remembered by this replica.",
  envelope:
    "Decode the verified body as JSON, XML, or multipart data, or keep the raw bytes.",
  filter:
    "Continue when all conditions match; otherwise acknowledge and drop the event, or reject it.",
  register_route:
    "Save an identifier-to-environment mapping so later requests can find the right backend.",
  route:
    "Choose a configured environment from request data or a saved route, subject to your routing rules.",
  lookup:
    "Read a value from a custom lookup provider; requires an application embedding Hookway.",
  batch:
    "Split a JSON batch into events, check each event’s environment, and run a delivery flow for each.",
  transform:
    "Map or remove fields, compare JSON changes, validate JSON, or clean up XML.",
  code: "Run sandboxed JavaScript to change the event body or set variables for later nodes.",
  switch:
    "Follow the first matching case, or the default output when no case matches.",
  if_else:
    "Follow the true output when conditions match and the false output otherwise; connect both.",
  http_request:
    "Call an HTTP API with the event or original request and use its response in later nodes.",
  publish:
    "Send the event or supported file content to a destination configured in this workspace.",
  respond:
    "Return a fixed HTTP response or the last API response to the original caller.",
};

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

/** Field copy keyed by "nodeType.field" then by bare field name. */
const NODE_FIELD_HELP: Record<string, string> = {
  "transform.mappings.from":
    "Event field to copy, such as body.id or body.customer.email.",
  "transform.mappings.to":
    "Event field to write, such as vars.event_id or body.customer_id.",
  "register_route.pattern":
    "Optional pattern the identifier must match before the route is saved.",
  "register_route.id_type":
    "Identifier category used in the routing store, such as order or customer. Must match the type used by later route lookups.",
  "batch.group_id_path":
    "Optional dotted path to a group ID in shared mode. Omitted uses the event ID.",
  "batch.event_id_path":
    "Dotted path to each event’s stable ID, such as id. Used for event identity and grouping.",
  "batch.environment_path":
    "Dotted path to the environment in each event, such as environment. Each value must be allowed by the routing profile.",
  "http_trigger.path":
    "Public URL path, for example /hooks/incoming. May include {params}.",
  "http_trigger.methods":
    "HTTP methods this trigger accepts. POST is the usual webhook default.",
  "loop_guard.max_hops":
    "How many Hookway hops are allowed before the request is rejected as a loop.",
  "ip_allowlist.cidrs": "CIDR ranges that may call this flow, e.g. 10.0.0.0/8.",
  "rate_limit.rpm":
    "Allowed requests per minute in this process. N pods means N times this limit.",
  "rate_limit.burst":
    "Maximum requests allowed in a burst. Omitted or 0 uses the requests-per-minute value.",
  "rate_limit.key_by":
    "Requests are counted by source_ip by default. Use header.Name to count by a request header.",
  "read_body.allow_empty":
    "Explicitly accept zero-byte requests, including GET/HEAD. Omit for webhooks that require a body.",
  "envelope.xml":
    "Optional XML parse budgets: max_bytes (default 1 MiB), max_depth (256), and max_nodes (100000). Raw XML proxies do not parse.",
  "envelope.max_total_bytes":
    "Maximum combined size of decoded multipart parts, in bytes. The original request and each part have separate limits.",
  "read_body.max_bytes":
    "Largest body to buffer. Larger requests are rejected before auth.",
  "auth.scheme":
    "Choose the signature, token, or credential format used by the sender.",
  "auth.secret":
    "Shared secret, often ${WEBHOOK_SECRET}. Use secret_ref instead of a literal in production.",
  "auth.signature_header":
    "Request header that carries the signature, e.g. X-Signature.",
  "auth.timestamp_tolerance":
    "How old a signed timestamp may be before auth fails, e.g. 5m.",
  "auth.remove_bearer":
    "Strip a leading Bearer prefix from the token before verifying.",
  "auth.expected_issuer": "JWT iss claim that must match. Leave empty to skip.",
  "auth.expected_audience":
    "JWT aud claim that must match. Leave empty to skip.",
  "auth.body_hash_claim": "JWT claim that must hash-match the raw body.",
  "replay_guard.ttl":
    "How long this replica remembers a request key, for example 5m. Omitted uses 5 minutes.",
  "replay_guard.max_entries":
    "In-memory cap for remembered keys in this process.",
  "replay_guard.key":
    "Optional request identity, such as {header.X-Event-Id}. Omitted hashes the original body bytes.",
  "envelope.body_format":
    "Choose how to decode the body: JSON, XML, multipart form data, or raw bytes (raw leaves the body empty).",
  "envelope.multipart":
    "Limits for body_format: multipart. Bounds how much of an upload is held in memory.",
  "filter.conditions": "All conditions must match for the event to continue.",
  "filter.on_no_match":
    "drop acknowledges 202 without publishing; reject fails the request.",
  "route.profile":
    "Routing rules configured in Config → Routing. The profile limits which environments this flow may use.",
  "route.store":
    "DynamoDB routing store configured in Config → Routing. Use it to find the environment saved for an identifier.",
  "route.candidates":
    "Request fields to try in order. Choose a source and path; store lookups also need an identifier type, such as order.",
  "route.fixed":
    "Always use this configured environment. Request values cannot change it.",
  "route.name":
    "Variable containing the selected environment (default: route). Also sets route_prefix, route_backend_host, route_account_id and route_credential_mode when the name is route.",
  "lookup.provider":
    "Name of a lookup provider supplied by the application embedding Hookway. The standalone server has no providers.",
  "lookup.key": "Key to look up, with variable templates such as {record_id}.",
  "lookup.name": "Variable name to store the result under.",
  "lookup.table": "Table or collection when the provider needs one.",
  "lookup.fallback": "Value to use when the lookup misses.",
  "lookup.negative_ttl":
    "How long to cache a missing result before trying the provider again, for example 30s.",
  "transform.kind":
    "Choose an operation that matches the parsed body format. JSON operations require JSON; XML operations require XML.",
  "transform.from":
    "First value to compare, using an event path such as body.before.",
  "transform.to":
    "Second value to compare, using an event path such as body.after.",
  "transform.name": "Variable name that receives the transform result.",
  "transform.mappings":
    "Pairs of source and destination event paths, such as body.id to vars.event_id.",
  "batch.child":
    "Flow run for each event, ending in one publish node. Child IDs start with this batch node ID plus a dot; triggers, nested batches and HTTP responses are not allowed.",
  "batch.profile":
    "Routing profile that decides which environments and account each event may use. Configure it in Config → Routing.",
  "batch.mode":
    "Shared processes events one at a time and acknowledges skipped events with 200. Dedicated accepts arrays, can run events concurrently, and returns 502 if an eligible event fails.",
  "batch.max_children":
    "Maximum events in one request (default: 1000; limit: 10000). A larger batch is rejected before any event is sent.",
  "batch.concurrency":
    "Events processed at once in dedicated mode (default: 8; limit: 64). Shared mode processes one at a time.",
  "batch.max_inflight":
    "Maximum active events across requests to this node (default: 64). The whole process also has a limit of 1024.",
  "batch.timeout":
    "Whole-batch deadline, default 30s, at most 2m. Caller cancellation stops pending work and active publishers.",
  "batch.receiving_environment":
    "Environment handled by this dedicated batch. Aliases resolve to the configured name; events for other allowed environments are skipped.",
  "transform.json_schema":
    "Inline offline JSON Schema, default draft 7. External resources are disabled; at most 256 KiB.",
  "transform.coercions":
    "Explicit scalar paths matching a single schema type. Union/anyOf ID representations remain unchanged.",
  "transform.fields":
    "Object dot paths removed by strip_fields. Only listed fields are removed.",
  "transform.xml_adapter":
    "XML element names and namespaces used by the ordered cleanup operation, plus placeholder text and the sequence attribute.",
  "filter.drop_reason":
    "Optional finite classification: dropped_unused_event_type or dropped_unsupported_event_type.",
  "transform.rules": "Named XML prune rules to apply.",
  "code.script":
    "JavaScript function body. Read input.body and input.vars; return an object with body and/or vars. Request metadata is redacted, and selected API response values may be available.",
  "code.timeout": "How long the script may run, e.g. 50ms.",
  "code.max_input_bytes":
    "Hard cap on the serialized input passed to the script.",
  "code.max_output_bytes": "Hard cap on what the script may return.",
  "code.max_call_stack": "Call-stack depth the sandbox will allow.",
  "switch.cases": "Each case is a labelled output port. The first match wins.",
  "switch.cases.match":
    "all requires every condition in the case; any requires one. Defaults to all.",
  "switch.cases.conditions":
    "Conditions for this case, joined by match. Use this instead of when, not alongside it.",
  "switch.cases.when":
    "A single condition for this case. The conditions list is the multi-condition form.",
  "switch.default":
    "Port used when no case matches. Must match a case port or a dedicated default handle.",
  "if_else.match":
    "all requires every condition to hold; any requires one. Defaults to all.",
  "if_else.conditions":
    "Conditions joined by match. They decide the true port from the false port.",
  "http_request.url":
    "URL to call. ${ENV} is resolved at startup; {variable} is resolved for each request. Choose this or a configured routing target.",
  "register_route.mode":
    "After upstream creation saves the route on a 200/201 API response and keeps that response for the caller. Direct saves a route from an incoming notification.",
  "register_route.id_path":
    "After upstream creation: name of a response field captured by the preceding API call. Direct: path to an identifier in the body or variables.",
  "register_route.max_attempts":
    "Maximum attempts to save the route within the timeout. If creation succeeded but saving failed, repair retries the save without creating the resource again.",
  "register_route.environment_variable":
    "Variable containing the environment already allowed by routing rules. The environment is checked again before saving the route.",
  "http_request.credential":
    "Choose a credential profile from Config → Credentials, or one per allowed environment. Secrets are exported as references; the profile handles session refresh and retry.",
  "http_request.require_headers":
    "Headers the caller must supply before the API call. Each must have exactly one nonempty value; these values are hidden from scripts and delivery capture.",
  "http_request.require_body_fields":
    "JSON fields the caller must supply before the API call.",
  "http_request.upstream":
    "Call the backend host of an environment allowed by the routing profile. Configure the environments and hosts in Config → Routing.",
  "http_request.upstream.port":
    "Optional port for the admitted upstream host. Omit it to use the scheme's default port.",
  "http_request.upstream.path":
    "Absolute path on the admitted upstream host, such as /v1/events/{event_id}. Do not include a query string or fragment.",
  "http_request.environment_variable":
    "Variable containing the selected environment, usually route. Its value must identify an environment allowed by the routing profile.",
  "http_request.method":
    "Fixed uppercase HTTP method; cannot accompany forward_method. Default POST.",
  "http_request.body_methods":
    "Only these methods carry a body. For example POST/PUT/PATCH, or add DELETE when required.",
  "http_request.query_mode":
    "all preserves repeated query values; first or last explicitly collapses them. Requires forward_query.",
  "http_request.remove_query":
    "Query names removed from the outbound copy, leaving ingress unchanged.",
  "http_request.header_mode":
    "forward copies approved inbound headers; replace starts with only configured outbound headers.",
  "http_request.remove_headers":
    "Inbound headers removed before configured headers are applied, e.g. the Hookway authentication token.",
  "http_request.remove_body_fields":
    "JSON dot paths removed from the outbound copy. Requires body: transformed and a JSON object.",
  "http_request.max_response_bytes":
    "Maximum upstream wire body (default 10 MiB, at most 64 MiB). Set 20971520 for a 20 MiB file budget.",
  "http_request.response_bindings":
    "Bounded non-sensitive response scalar paths and optional status variable. Selection failure preserves the upstream reply; scripts receive no raw response body or headers.",
  "http_request.max_json_bytes":
    "JSON response selection budget (default 64 KiB, maximum 1 MiB), separate from the relay body limit.",
  "http_request.redirects":
    "Optional max_hops (1..10). Redirects stay on the same scheme and authority and are revalidated; default returns redirects to the caller.",
  "http_request.on":
    "network retries transport/read failures only; network_and_status also retries 429/5xx. Creation routes should retain one attempt.",
  "http_request.methods":
    "Optional retry method allowlist. Methods outside it receive one attempt.",
  "http_request.total_timeout":
    "Deadline shared by all attempts and backoff, separate from each attempt's timeout.",
  "http_request.forward_method":
    "Send the original request method instead of POST.",
  "http_request.forward_query":
    "Copy the original query string onto the upstream URL.",
  "http_request.forward_headers": "Request header names to copy upstream.",
  "http_request.timeout": "Upstream deadline, e.g. 10s.",
  "http_request.headers": "Extra headers to send. Values may use ${vars}.",
  "http_request.error_on_status":
    "Status classes that fail this node, e.g. 5xx.",
  "http_request.body":
    "raw sends the original bytes; transformed sends the current event body.",
  "http_request.retry":
    "Optional: re-attempt transport errors, timeouts, 429, and 5xx. Leave empty for a single attempt.",
  "publish.destination": "Id of an entry in Destinations.",
  "publish.target":
    "Queue, topic, routing key, webhook path, or Lambda function name/ARN, depending on the selected destination.",
  "publish.create":
    "Declare the queue/topic at startup when the backend supports it (not Lambda).",
  "publish.message_attributes":
    "SNS/SQS message attributes (String, Number, String.Array, or Binary encoded as base64; SQS custom type suffixes supported).",
  "auth.timestamp_header":
    "Separate timestamp header for header-based HMAC or ECDSA. Must differ from the signature header.",
  "auth.timestamp_unit":
    "Seconds or milliseconds since Unix epoch. Required for request signature schemes.",
  "auth.timestamp_policy":
    "symmetric (default) checks past and future; age checks only maximum age; unchecked explicitly skips freshness checks while still requiring a valid signed timestamp.",
  "auth.body_representation":
    "raw (default) signs original bytes. json_js is an explicit JavaScript parse/stringify representation for hmac_sha256_headers_dot only; raw bytes remain available downstream.",
  "auth.signature_label":
    "Repeated digest label for hmac_sha256_ts_dot_multi, default s. Timestamp uses t. At most 16 candidates.",
  "auth.json_field":
    "Select a top-level string field from a JSON secret bundle before key validation and rotation.",
  "publish.queue_owner_account_id":
    "Optional 12-digit account ID for an existing cross-account SQS queue; requires create: false and appropriate AWS permissions.",
  "publish.topic_arn":
    "Explicit ARN for an existing SNS topic whose name equals target; requires create: false. Resolves directly without ListTopics.",
  "publish.group_id":
    "Optional fair-queue group identity for standard SQS queues and SNS topics. FIFO targets use fifo.group_id.",
  "publish.sqs":
    "SQS per-message options. delay_seconds (0..900) is valid only on standard queues.",
  "publish.sns":
    "SNS per-message options: subject for standard topics; message_structure: json for protocol-specific messages with a string default field.",
  "publish.delay_seconds":
    "Override the queue delay for this standard SQS message, from 0 to 900 seconds. FIFO delays belong in resource_attributes.",
  "publish.subject":
    "Email subject on a standard SNS topic; fewer than 100 bytes without control characters.",
  "publish.message_structure":
    "json sends protocol-specific messages. Wire body must be a JSON object with string values and a default key.",
  "publish.resource_attributes":
    "AWS queue or topic settings, such as encryption and retention. Omitted settings are left unchanged. Nodes using the same target must agree; the conflict policy controls existing resources.",
  "auth.remove_prefix":
    "Optional exact, case-sensitive prefix to remove before token comparison. Bare tokens remain valid. Requires exact auth and cannot be combined with remove_bearer.",
  "publish.wire_body":
    "SQS/SNS bytes: envelope (default), raw (original UTF-8), json (transformed JSON), or json_js (original request parsed and stringified with JavaScript semantics).",
  "publish.fifo":
    "FIFO targets need a group ID and either an explicit deduplication ID or content-based deduplication. Choose where each ID comes from; replay preserves these IDs.",
  "publish.source":
    "FIFO identity source: static template, body field, header, variable, SHA-256 of original bytes, or SHA-256 of original JSON parsed and stringified with JavaScript semantics.",
  "publish.value":
    "Literal identity or {variable} template, used only with source: static.",
  "publish.path":
    "Body dot path, header name or variable name for the selected source. Omit for static and hash sources.",
  "publish.fallback":
    "Optional literal or {variable} template when the selected identity is missing. Cannot be combined with fallback_hash.",
  "publish.fallback_hash":
    "Hash the original raw or JavaScript JSON representation when the selected body/header/variable identity is missing.",
  "publish.hash_length":
    "Hex characters to retain for a hash source or fallback hash (1..64). Omit to keep all 64. This does not truncate an existing field identity.",
  "publish.max_attempts":
    "Total broker attempts including the first (1..10). Required when an explicit retry policy is configured.",
  "publish.initial_backoff":
    "Wait before the second broker attempt, doubled after each retry. Defaults to 100ms.",
  "publish.max_backoff":
    "Maximum wait between broker attempts. Defaults to 1s; must be at least the initial backoff and at most 1m.",
  "publish.retry":
    "Explicit broker attempts, including the first attempt (1..10). Backoffs default to 100ms and 1s. SDK retries are disabled within these attempts; server publish timeout bounds the total.",
  "publish.retry.initial_backoff":
    "Wait before the second broker attempt. Defaults to 100ms.",
  "publish.retry.max_backoff":
    "Maximum wait between broker attempts. Defaults to 1s.",
  "publish.group_id.source":
    "Choose static, body, header, variable, or a supported request hash as the group identity source.",
  "publish.group_id.value":
    "Identity template used when source is static. Supports {variable} substitutions.",
  "publish.group_id.path":
    "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
  "publish.fifo.group_id.source":
    "Choose static, body, header, variable, or a supported request hash as the FIFO group identity source.",
  "publish.fifo.group_id.value":
    "Identity template used when source is static. Supports {variable} substitutions.",
  "publish.fifo.group_id.path":
    "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
  "publish.fifo.deduplication_id.source":
    "Choose static, body, header, variable, or a supported request hash as the deduplication identity source.",
  "publish.fifo.deduplication_id.value":
    "Identity template used when source is static. Supports {variable} substitutions.",
  "publish.fifo.deduplication_id.path":
    "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
  "auth.source.kind":
    "Choose where the signature or token is read: a request header or query parameter.",
  "auth.source.key":
    "Name of the header or query parameter that carries the signature or token.",
  "filter.conditions.source":
    "Predicate values come from request headers or the decoded event body.",
  "filter.conditions.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
  "if_else.conditions.source":
    "Predicate values come from request headers or the decoded event body.",
  "if_else.conditions.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
  "switch.cases.when.source":
    "Predicate values come from request headers or the decoded event body.",
  "switch.cases.when.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
  "switch.cases.conditions.source":
    "Predicate values come from request headers or the decoded event body.",
  "switch.cases.conditions.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
  "publish.payload":
    "envelope sends the event as JSON. object sends raw file bytes and is supported only by S3 and Redis key/value destinations.",
  "respond.status": "HTTP status to return. Omit if passthrough is on.",
  "respond.headers": "Response headers to add.",
  "respond.body": "Response body. Ignored when passthrough is on.",
  "respond.forward_set_cookie":
    "Opt in to returning upstream Set-Cookie values. Disabled by default; review cookie domain/path for the public origin.",
  "respond.forward_headers":
    "Optional allowlist of end-to-end response headers. Framing is computed by Hookway; Set-Cookie also requires forward_set_cookie.",
  "respond.passthrough":
    "Return the last upstream http_request response instead of a fixed status/body.",
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
  // Keyed "<parent>.<child>" because SchemaForm's nestedHelp asks for the
  // qualified name first, and nodeFieldHelp only consults this map (not
  // NODE_FIELD_HELP) for a dotted field.
  "retry.max_attempts":
    "Total attempts including the first. 1 (or empty) means no retry. Max 10.",
  "multipart.max_parts":
    "Parts allowed before the request is rejected with 413. Default 16, max 256.",
  "multipart.max_part_bytes":
    "Per-part size cap in bytes. Default 1048576 (1 MiB).",
  "multipart.include_content":
    "Carry each file's bytes as base64. Turn off to keep metadata only and copy nothing.",
  "retry.initial_backoff":
    "Wait before the second HTTP request attempt; waits double with jitter. Defaults to 500ms.",
  "retry.max_backoff":
    "Maximum wait between HTTP request attempts, including a 429 Retry-After. Defaults to 30s.",
  "conditions.path":
    "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
};

export function nodeHelp(type: string): string {
  return NODE_HELP[type] ?? "";
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
    ...(type === "transform"
      ? {
          from: "First value path",
          to: "Second value path",
          mappings: "Field mappings",
          "mappings.from": "Source path",
          "mappings.to": "Destination path",
          fields: "Fields to remove",
          rules: "XML cleanup rules",
          json_schema: "JSON Schema",
          coercions: "Convert values before validation",
          xml_adapter: "XML cleanup selectors",
        }
      : {}),
    ...(type === "batch"
      ? {
          environment_path: "Environment field path",
          event_id_path: "Event ID field path",
          group_id_path: "Group ID field path",
          max_children: "Maximum events per batch",
          concurrency: "Concurrent events",
          max_inflight: "Maximum active events",
        }
      : {}),
    ...(type === "http_request"
      ? {
          body: "Request body to send",
          url: "API URL",
          environment_variable: "Environment variable name",
          forward_query: "Forward query parameters",
          query_mode: "Repeated query parameters",
          remove_query: "Query parameters to remove",
          header_mode: "Request header behavior",
          forward_headers: "Headers to forward",
          headers: "Headers to set",
          remove_headers: "Headers to remove",
          body_methods: "HTTP methods that send a body",
          remove_body_fields: "Body fields to remove",
          require_headers: "Required caller headers",
          require_body_fields: "Required caller JSON fields",
          response_bindings: "Capture API response values",
          max_response_bytes: "Maximum API response size (bytes)",
          error_on_status: "HTTP statuses treated as failures",
          allow_private_targets: "Allow private network targets",
        }
      : {}),
    ...(type === "route"
      ? {
          candidates: "Fields to try for routing",
          id_type: "Identifier type",
          pattern: "Identifier pattern",
        }
      : {}),
    ...(type === "register_route"
      ? {
          environment_variable: "Environment variable name",
          id_source: "Identifier source",
          id_path: "Identifier field or binding",
          id_type: "Identifier type",
          pattern: "Identifier pattern",
          max_attempts: "Maximum save attempts",
          require_headers: "Required caller headers",
        }
      : {}),
    ...(type === "publish"
      ? {
          target: "Delivery target",
          payload: "Payload to send",
          create: "Create the queue or topic at startup",
          wire_body: "Message body format",
          resource_attributes: "AWS queue or topic settings",
          message_attributes: "Message attributes",
        }
      : {}),
    ...(type === "respond"
      ? {
          status: "HTTP status code",
          headers: "Response headers",
          body: "Response body",
          forward_headers: "API response headers to return",
          forward_set_cookie: "Return API response cookies",
        }
      : {}),
  };
}

export function nodeFieldHelp(
  type: string | undefined,
  field: string,
): string | undefined {
  if (type) {
    const specific = NODE_FIELD_HELP[`${type}.${field}`];
    if (specific) return specific;
  }
  if (COMMON_FIELD_HELP[field]) return COMMON_FIELD_HELP[field];
  const leaf = field.includes(".")
    ? field.slice(field.lastIndexOf(".") + 1)
    : field;
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
    "envelope sends the Hookway event; proxy forwards the original request.",
  mode: "Redis topology: single, sentinel, or cluster.",
  key_prefix: "Prefix applied to Redis keys.",
};

export function destFieldHelp(field: string): string | undefined {
  return DEST_FIELD_HELP[field] ?? COMMON_FIELD_HELP[field];
}

export function missingNodeHelp(): string[] {
  return NODE_TYPES.filter((t) => !NODE_HELP[t]);
}
