import type { IconType } from "react-icons";
import type nodeCatalog from "./nodeSchema.json";
import {
  TbArrowBackUp,
  TbPlugConnected,
  TbBraces,
  TbStack2,
  TbArrowsSplit,
  TbCode,
  TbDatabaseSearch,
  TbDatabasePlus,
  TbDice,
  TbEye,
  TbRoute,
  TbFileText,
  TbFilter,
  TbGauge,
  TbGitBranch,
 TbFlag,
  TbApi,
  TbHistoryOff,
  TbJson,
  TbListCheck,
  TbLockOpen,
  TbPackage,
  TbRepeatOff,
  TbSend,
  TbShieldLock,
  TbTransform,
  TbWebhook,
} from "react-icons/tb";
import { humanLabel } from "../shared/ui/text";

// Presentation stays independent of form components and runtime validation.
// Go owns schemas and capabilities; this record owns each node's authoring UI.
type NodeEditor =
  | "mcp"
  | "schema"
  | "simple"
  | "auth"
  | "protection"
  | "json-validation"
  | "processing"
  | "publish"
  | "respond"
  | "http"
  | "routing"
  | "public";
type Options = Record<string, unknown>;
export interface NodeDescriptor {
  label: string;
  icon: IconType;
  help: string;
  editor: NodeEditor;
  initialOptions?: (id: string) => Options;
  summary?: (options: Options) => string;
  labelledPorts?: (options: Options) => string[];
  fieldHelp?: Record<string, string>;
  fieldLabels?: Record<string, string>;
  simple?: {
    primaryFields: string[];
    summary: string;
    fieldHelp: Record<string, string>;
  };
}

// The generated catalog makes a missing or misspelled descriptor a type error.
export const NODE_DESCRIPTORS: Record<keyof typeof nodeCatalog, NodeDescriptor> = {
  mcp_trigger: {
    label: "MCP trigger", icon: TbPlugConnected, editor: "mcp",
    help: "Expose an authenticated MCP endpoint at a path you choose. Add endpoints as separate flows, each with its own tools, authentication and permissions.",
    fieldLabels: { path: "Endpoint path", public_url: "Public URL", allowed_origins: "Allowed browser origins", max_inflight: "Concurrent calls" },
    fieldHelp: {
      path: "Choose a distinct URL path, such as /services/mcp or /utilities/mcp.",
      public_url: "The public HTTPS URL for this endpoint. Its path must match Endpoint path. HTTP is allowed for localhost development.",
      allowed_origins: "Exact browser origins allowed to connect, including the builder origin when using Test & connect.",
      max_inflight: "Maximum active tool calls for this endpoint. Defaults to 32; all endpoints also share process admission limits.",
    },
    initialOptions: () => ({ path: "/mcp", public_url: "https://hookway.example.com/mcp", auth: { api_keys: { reader: { secret_ref: { provider: "env", name: "MCP_READER_TOKEN" } } } }, tools: [] }),
    summary: (o) => `${String(o.path ?? "/mcp")} · ${Array.isArray(o.tools) ? o.tools.length : 0} tools`,
    labelledPorts: (o) => Array.isArray(o.tools) ? o.tools.flatMap((tool) => {
      if (!tool || typeof tool !== "object" || Array.isArray(tool)) return [];
      const name = (tool as Options).name;
      return typeof name === "string" && name ? [name] : [];
    }) : [],
  },
  mcp_result: {
    label: "MCP result", icon: TbBraces, editor: "schema",
    help: "Return schema-validated JSON from the selected tool branch. Choose the native body, upstream JSON, or a constant, with optional field projection.",
    initialOptions: () => ({source:"body"}),
    summary: (o) => `Return ${String(o.source ?? "body")}`,
  },
  launchdarkly: {
 label: "LaunchDarkly flags", icon: TbFlag, editor: "schema",
 help: "Evaluate named feature flags for a tenant or user. Route using typed results without changing the webhook body.",
 initialOptions: (id) => ({name:id.replace(/[^A-Za-z0-9_]/g,"_"),connection:"",context:{kind:"user",key:{source:"body",path:"tenant.id"}},flags:{new_processor:{key:"use-new-processor",type:"boolean",fallback:false}}}),
 summary: (o) => `${String(o.connection ?? "Choose connection")} · ${Object.keys((o.flags as object) ?? {}).length} flags`,
 },
 http_trigger: {
    label: "HTTP endpoint",
    icon: TbWebhook,
    help: "Start the flow when a request reaches the configured HTTP path.",
    editor: "simple",
    initialOptions: (id) => ({ methods: ["POST"], path: `/webhooks/${id}` }),
    summary: (options) => String(options.path ?? ""),
    fieldHelp: {
      "path": "Public URL path, for example /hooks/incoming. May include {params}.",
      "methods": "HTTP methods this trigger accepts. POST is the usual webhook default.",
    },
    simple: {
      primaryFields: ["path"],
      summary: "Use one endpoint per flow. Connect request checks, then Read request body, then a node that verifies or skips authentication.",
      fieldHelp: {
        path: "HTTP route, such as /events or /events/{record_id}. Route captures become variables. Empty method selection uses POST.",
      },
    },
  },
  loop_guard: {
    label: "Prevent request loops",
    icon: TbRepeatOff,
    help: "Reject requests that have passed through Hookway too many times.",
    editor: "simple",
    fieldHelp: {
      "max_hops": "How many Hookway hops are allowed before the request is rejected as a loop.",
    },
    simple: {
      primaryFields: ["max_hops"],
      summary: "Place this before Read request body. Requests are rejected at the configured hop limit; omitted or zero uses 3.",
      fieldHelp: {
        max_hops: "Maximum forwarded hops. Omitted or 0 uses 3; requests with a hop count at or above the effective limit are rejected.",
      },
    },
  },
  ip_allowlist: {
    label: "Allow client IPs",
    icon: TbListCheck,
    help: "Accept requests only from the client IP addresses or networks you allow.",
    editor: "simple",
    fieldHelp: {
      "cidrs": "CIDR ranges that may call this flow, e.g. 10.0.0.0/8.",
    },
    simple: {
      primaryFields: ["cidrs"],
      summary: "Accept requests only from the listed IP addresses or networks. Place this gate before read_body. Configure trusted proxies separately so the client address is reliable.",
      fieldHelp: {
        cidrs: "Comma-separated IP addresses or CIDR networks, such as 192.0.2.10, 198.51.100.0/24, 2001:db8::/32. At least one entry is required.",
      },
    },
  },
  rate_limit: {
    label: "Limit request rate",
    icon: TbGauge,
    help: "Control bursts or rolling quotas by IP, header, or verified identity using a selected memory, Redis, or PostgreSQL store.",
    editor: "protection",
    initialOptions: () => ({ rpm: 60 }),
    fieldHelp: {
      "rpm": "Continuous token refill rate. Memory applies per process; Redis and PostgreSQL coordinate the configured policy across instances.",
      "burst": "Maximum requests allowed in a burst. Omitted or 0 uses the requests-per-minute value.",
      "key_by": "Requests are counted by source_ip by default. Use header.Name to count by a request header.",
    },
  },
  read_body: {
    label: "Read request body",
    icon: TbFileText,
    help: "Read the original request bytes and reject bodies above your size limit.",
    editor: "simple",
    initialOptions: () => ({ max_bytes: 1048576 }),
    fieldHelp: {
      "allow_empty": "Explicitly accept zero-byte requests, including GET/HEAD. Omit for webhooks that require a body.",
      "max_bytes": "Largest body to buffer. Larger requests are rejected before auth.",
    },
    simple: {
      primaryFields: ["max_bytes", "allow_empty"],
      summary: "Set the maximum request size here before verifying the sender. This node enforces the limit; server.max_body_bytes is used for validation warnings and does not override it.",
      fieldHelp: {
        max_bytes: "Positive limit in bytes. 1 MiB = 1048576 bytes. Account for larger multipart wire payloads separately from decoded file limits.",
        allow_empty: "Permit an empty request body, for example when accepting GET or HEAD. Authentication and subsequent body parsing must also support that request.",
      },
    },
  },
  auth: {
    label: "Verify signature or token",
    icon: TbShieldLock,
    help: "Check the sender’s signature, token, or HTTP Basic credentials before processing the request.",
    editor: "auth",
    initialOptions: () => ({
      scheme: "hmac_sha256_hex",
      source: { kind: "header", key: "X-Signature" },
      secret_ref: { provider: "env", name: "WEBHOOK_SECRET" },
    }),
    summary: (options) => String(options.scheme ?? ""),
    fieldHelp: {
      "scheme": "Choose the signature, token, or credential format used by the sender.",
      "secret": "Shared secret, often ${WEBHOOK_SECRET}. Use secret_ref instead of a literal in production.",
      "signature_header": "Request header that carries the signature, e.g. X-Signature.",
      "timestamp_tolerance": "How old a signed timestamp may be before auth fails, e.g. 5m.",
      "remove_bearer": "Strip a leading Bearer prefix from the token before verifying.",
      "expected_issuer": "JWT iss claim that must match. Leave empty to skip.",
      "expected_audience": "JWT aud claim that must match. Leave empty to skip.",
      "credential_id": "Stable identifier for these verified credentials. Required when a downstream rate limit uses a verified identity. Changing it changes quota identity.",
      "tenant_claim": "Verified JWT claim containing the tenant ID, such as tenant_id. Requires configured expected issuer and audience for tenant quotas.",
      "body_hash_claim": "JWT claim that must hash-match the raw body.",
      "timestamp_header": "Separate timestamp header for header-based HMAC or ECDSA. Must differ from the signature header.",
      "timestamp_unit": "Seconds or milliseconds since Unix epoch. Required for request signature schemes.",
      "timestamp_policy": "symmetric (default) checks past and future; age checks only maximum age; unchecked explicitly skips freshness checks while still requiring a valid signed timestamp.",
      "body_representation": "raw (default) signs original bytes. json_js is an explicit JavaScript parse/stringify representation for hmac_sha256_headers_dot only; raw bytes remain available downstream.",
      "signature_label": "Repeated digest label for hmac_sha256_ts_dot_multi, default s. Timestamp uses t. At most 16 candidates.",
      "json_field": "Select a top-level string field from a JSON secret bundle before key validation and rotation.",
      "remove_prefix": "Optional exact, case-sensitive prefix to remove before token comparison. Bare tokens remain valid. Requires exact auth and cannot be combined with remove_bearer.",
      "source.kind": "Choose where the signature or token is read: a request header or query parameter.",
      "source.key": "Name of the header or query parameter that carries the signature or token.",
    },
  },
  no_auth: {
    label: "Skip authentication",
    icon: TbLockOpen,
    help: "Accept requests without verifying the sender; use only when unauthenticated access is intended.",
    editor: "public",
  },
  replay_guard: {
    label: "Reject duplicate requests",
    icon: TbHistoryOff,
    help: "Reject duplicate requests within a time window, locally or across instances using a shared store.",
    editor: "protection",
    fieldHelp: {
      "ttl": "Duplicate window starting at admission, for example 5m. Duplicates do not extend expiry.",
      "max_entries": "Maximum live reservations per policy. New identities fail closed at capacity.",
      "key": "Optional request identity, such as {header.X-Event-Id}. Omitted hashes the original body bytes.",
    },
  },
  envelope: {
    label: "Parse request body",
    icon: TbPackage,
    help: "Decode the verified body as JSON, XML, or multipart data, or keep the raw bytes.",
    editor: "processing",
    initialOptions: () => ({ body_format: "json" }),
    fieldHelp: {
      "xml": "Optional XML parse budgets: max_bytes (default 1 MiB), max_depth (256), and max_nodes (100000). Raw XML proxies do not parse.",
      "max_total_bytes": "Maximum combined size of decoded multipart parts, in bytes. The original request and each part have separate limits.",
      "body_format": "Choose how to decode the body: JSON, XML, multipart form data, or raw bytes (raw leaves the body empty).",
      "multipart": "Limits for body_format: multipart. Bounds how much of an upload is held in memory.",
    },
  },
  inspect: {
    label: "Inspect",
    icon: TbEye,
    help: "View the current webhook in the replay console. Requires Replay configuration. Use after Sample on a separate observation branch; uploads are best effort.",
    editor: "schema",
    initialOptions: () => ({}),
  },
  sample: {
    label: "Sample requests",
    icon: TbDice,
    help: "Randomly keep a fraction of events after envelope creation. Before a batch, sample the whole request; inside a batch child flow, sample each item independently.",
    editor: "schema",
    initialOptions: () => ({ rate: 1 }),
    summary: (options) => typeof options.rate === "number" && Number.isFinite(options.rate)
      ? `${Number((options.rate * 100).toPrecision(12))}% kept`
      : "",
    fieldLabels: { rate: "Sampling rate" },
    fieldHelp: {
      rate: "Required fraction kept, from 0 to 1 inclusive. 0 drops all; 1 keeps all; 0.2 keeps roughly 20%; 0.001 keeps roughly 0.1%. Smaller positive fractions are supported. Each execution, including retries and replays, makes an independent random decision. Sampled-out events stop this branch successfully and receive the default HTTP 202 acknowledgment.",
    },
  },
  filter: {
    label: "Filter events",
    icon: TbFilter,
    help: "Continue when all conditions match; otherwise acknowledge and drop the event, or reject it.",
    editor: "processing",
    initialOptions: () => ({ conditions: [{ source: "body", path: "", operator: "eq" }] }),
    summary: (options) => {
      const conditions = Array.isArray(options.conditions) ? options.conditions : [];
      const first = conditions[0] as {
        path?: string;
        operator?: string;
      } | undefined;
      return first?.path ? `${first.path} ${first.operator ?? ""}`.trim() : "";
    },
    fieldHelp: {
      "conditions": "All conditions must match for the event to continue.",
      "on_no_match": "drop acknowledges 202 without publishing; reject fails the request.",
      "drop_reason": "Optional finite classification: dropped_unused_event_type or dropped_unsupported_event_type.",
      "conditions.source": "Predicate values come from request headers or the decoded event body.",
      "conditions.path": "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
    },
  },
  register_route: {
    label: "Save a route",
    icon: TbDatabasePlus,
    help: "Save an identifier-to-environment mapping so later requests can find the right backend.",
    editor: "routing",
    fieldHelp: {
      "pattern": "Optional pattern the identifier must match before the route is saved.",
      "id_type": "Identifier category used in the routing store, such as order or customer. Must match the type used by later route lookups.",
      "mode": "After upstream creation saves the route on a 200/201 API response and keeps that response for the caller. Direct saves a route from an incoming notification.",
      "id_path": "After upstream creation: name of a response field captured by the preceding API call. Direct: path to an identifier in the body or variables.",
      "max_attempts": "Maximum attempts to save the route within the timeout. If creation succeeded but saving failed, repair retries the save without creating the resource again.",
      "environment_variable": "Variable containing the environment already allowed by routing rules. The environment is checked again before saving the route.",
    },
    fieldLabels: {
      environment_variable: "Environment variable name",
      id_source: "Identifier source",
      id_path: "Identifier field or binding",
      id_type: "Identifier type",
      pattern: "Identifier pattern",
      max_attempts: "Maximum save attempts",
      require_headers: "Required caller headers",
    },
  },
  route: {
    label: "Select an environment",
    icon: TbRoute,
    help: "Choose a configured environment from request data or a saved route, subject to your routing rules.",
    editor: "routing",
    fieldHelp: {
      "profile": "Routing rules configured in Config → Routing. The profile limits which environments this flow may use.",
      "store": "DynamoDB routing store configured in Config → Routing. Use it to find the environment saved for an identifier.",
      "candidates": "Request fields to try in order. Choose a source and path; store lookups also need an identifier type, such as order.",
      "fixed": "Always use this configured environment. Request values cannot change it.",
      "name": "Variable containing the selected environment (default: route). Also sets route_prefix, route_backend_host, route_account_id and route_credential_mode when the name is route.",
    },
    fieldLabels: {
      candidates: "Fields to try for routing",
      id_type: "Identifier type",
      pattern: "Identifier pattern",
    },
  },
  lookup: {
    label: "Look up a value",
    icon: TbDatabaseSearch,
    help: "Read a value from a named HTTP, PostgreSQL or DynamoDB lookup provider.",
    editor: "simple",
    fieldHelp: {
      "provider": "Name of a provider configured in the Lookups workspace.",
      "key": "Key to look up, with variable templates such as {record_id}.",
      "name": "Variable name to store the result under.",
      "table": "Table or collection when the provider needs one.",
      "fallback": "Value to use when the lookup misses.",
      "negative_ttl": "How long to cache a missing result before trying the provider again, for example 30s.",
    },
    simple: {
      primaryFields: ["key", "name", "fallback"],
      summary: "Read a string from an HTTP API, PostgreSQL query or DynamoDB table. Configure a named provider in Lookups, then use its result in later nodes.",
      fieldHelp: {
        provider: "Name of a configured workspace lookup provider.",
        key: "Use a variable template such as {record_id}, or a body selection such as {body.record_id}.",
        name: "Variable that receives the result; omitted uses this node's ID.",
        table: "Provider-specific table setting. Confirm its meaning with the injected provider.",
        cache: "Optional cache configuration for providers that support it. This authoring block does not supply a lookup provider.",
        "cache.ttl": "Positive Go duration for cached successful lookups; interpreted by the injected provider.",
        "cache.max_entries": "Maximum successful lookup entries for providers supporting this cache configuration.",
        fallback: "Optional string returned when a lookup finds no result. Omitted causes the lookup to fail.",
        negative_ttl: "Nonnegative Go duration for caching misses when supported by the provider. 0 disables negative caching.",
      },
    },
  },
  batch: {
    label: "Process a batch",
    icon: TbStack2,
    help: "Split a JSON batch into events, check each event’s environment, and run a delivery flow for each.",
    editor: "processing",
    fieldHelp: {
      "group_id_path": "Optional dotted path to a group ID in shared mode. Omitted uses the event ID.",
      "event_id_path": "Dotted path to each event’s stable ID, such as id. Used for event identity and grouping.",
      "environment_path": "Dotted path to the environment in each event, such as environment. Each value must be allowed by the routing profile.",
      "child": "Flow run for each event, ending in one publish node. Child IDs start with this batch node ID plus a dot; triggers, nested batches and HTTP responses are not allowed.",
      "profile": "Routing profile that decides which environments and account each event may use. Configure it in Config → Routing.",
      "mode": "Shared processes events one at a time and acknowledges skipped events with 200. Dedicated accepts arrays, can run events concurrently, and returns 502 if an eligible event fails.",
      "max_children": "Maximum events in one request (default: 1000; limit: 10000). A larger batch is rejected before any event is sent.",
      "concurrency": "Events processed at once in dedicated mode (default: 8; limit: 64). Shared mode processes one at a time.",
      "max_inflight": "Maximum active events across requests to this node (default: 64). The whole process also has a limit of 1024.",
      "timeout": "Whole-batch deadline, default 30s, at most 2m. Caller cancellation stops pending work and active publishers.",
      "receiving_environment": "Environment handled by this dedicated batch. Aliases resolve to the configured name; events for other allowed environments are skipped.",
    },
    fieldLabels: {
      environment_path: "Environment field path",
      event_id_path: "Event ID field path",
      group_id_path: "Group ID field path",
      max_children: "Maximum events per batch",
      concurrency: "Concurrent events",
      max_inflight: "Maximum active events",
    },
  },
  transform: {
    label: "Transform data",
    icon: TbTransform,
    help: "Map or remove fields, compare JSON changes, validate JSON, or clean up XML.",
    editor: "processing",
    fieldHelp: {
      "mappings.from": "Event field to copy, such as body.id or body.customer.email.",
      "mappings.to": "Event field to write, such as vars.event_id or body.customer_id.",
      "kind": "Choose an operation that matches the parsed body format. JSON operations require JSON; XML operations require XML.",
      "from": "First value to compare, using an event path such as body.before.",
      "to": "Second value to compare, using an event path such as body.after.",
      "name": "Variable name that receives the transform result.",
      "mappings": "Pairs of source and destination event paths, such as body.id to vars.event_id.",
      "json_schema": "Inline offline JSON Schema, default draft 7. External resources are disabled; at most 256 KiB.",
      "coercions": "Explicit scalar paths matching a single schema type. Union/anyOf ID representations remain unchanged.",
      "fields": "Object dot paths removed by strip_fields. Only listed fields are removed.",
      "xml_adapter": "XML element names and namespaces used by the ordered cleanup operation, plus placeholder text and the sequence attribute.",
      "rules": "Named XML prune rules to apply.",
    },
    fieldLabels: {
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
    },
  },
  json_validate: {
    label: "Validate JSON",
    icon: TbJson,
    help: "Check a parsed JSON event against a required schema; optional conversions are explicit.",
    editor: "json-validation",
    fieldHelp: {
      "schema": "Required inline JSON Schema. Draft 7 is the default; local references and formats work offline. External resources are disabled. Defaults are not inserted and extra properties are not removed.",
      "coercions": "Optional explicit scalar conversions before validation. Each path must point to a property with one matching type.",
    },
  },
  code: {
    label: "Run JavaScript",
    icon: TbCode,
    help: "Run sandboxed JavaScript to change the event body or set variables for later nodes.",
    editor: "simple",
    fieldHelp: {
      "script": "JavaScript function body. Read input.body and input.vars; return an object with body and/or vars. Request metadata is redacted, and selected API response values may be available.",
      "timeout": "How long the script may run, e.g. 50ms.",
      "max_input_bytes": "Hard cap on the serialized input passed to the script.",
      "max_output_bytes": "Hard cap on what the script may return.",
      "max_call_stack": "Call-stack depth the sandbox will allow.",
    },
    simple: {
      primaryFields: ["script"],
      summary: "Run a short JavaScript function body with isolated input.body and input.vars. Input headers and query values are redacted; selected upstream response values may also be available. Return an object containing body and/or vars; returned variable values are converted to strings.",
      fieldHelp: {
        script: "JavaScript function body with top-level return. No require, fetch, timers or filesystem access. For XML input, preserve the tagged XML node-tree shape.",
        timeout: "Positive Go duration, such as 1s; omitted uses the generated default.",
        max_input_bytes: "Maximum JSON-encoded script input in bytes, including body and metadata.",
        max_output_bytes: "Maximum JSON-encoded script result in bytes.",
        max_call_stack: "Maximum JavaScript call-stack depth; omitted uses the generated default.",
      },
    },
  },
  switch: {
    label: "Switch between routes",
    icon: TbArrowsSplit,
    help: "Follow the first matching case, or the default output when no case matches.",
    editor: "processing",
    initialOptions: () => ({
      cases: [
        { port: "matched", when: { source: "body", path: "", operator: "eq" } },
      ],
      default: "default",
    }),
    labelledPorts: switchPorts,
    fieldHelp: {
      "cases": "Each case is a labelled output port. The first match wins.",
      "cases.match": "all requires every condition in the case; any requires one. Defaults to all.",
      "cases.conditions": "Conditions for this case, joined by match. Use this instead of when, not alongside it.",
      "cases.when": "A single condition for this case. The conditions list is the multi-condition form.",
      "default": "Port used when no case matches. Must match a case port or a dedicated default handle.",
      "cases.when.source": "Predicate values come from request headers or the decoded event body.",
      "cases.when.path": "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
      "cases.conditions.source": "Predicate values come from request headers or the decoded event body.",
      "cases.conditions.path": "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
    },
  },
  if_else: {
    label: "Branch with if / else",
    icon: TbGitBranch,
    help: "Follow the true output when conditions match and the false output otherwise; connect both.",
    editor: "processing",
    initialOptions: () => ({
      match: "all",
      conditions: [{ source: "body", path: "", operator: "eq" }],
    }),
    labelledPorts: () => ["true", "false"],
    fieldHelp: {
      "match": "all requires every condition to hold; any requires one. Defaults to all.",
      "conditions": "Conditions joined by match. They decide the true port from the false port.",
      "conditions.source": "Predicate values come from request headers or the decoded event body.",
      "conditions.path": "For header, enter a header name. For body, enter a dotted JSON path (event.type) or XML path (order.@id).",
    },
  },
  http_request: {
    label: "Call an HTTP API",
    icon: TbApi,
    help: "Call an HTTP API with the event or original request and use its response in later nodes.",
    editor: "http",
    summary: (options) => String(options.url ?? ""),
    fieldHelp: {
      "url": "URL to call. ${ENV} is resolved at startup; {variable} is resolved for each request. Choose this or a configured routing target.",
      "credential": "Choose a credential profile from Config → Credentials, or one per allowed environment. Secrets are exported as references; the profile handles session refresh and retry.",
      "require_headers": "Headers the caller must supply before the API call. Each must have exactly one nonempty value; these values are hidden from scripts and delivery capture.",
      "require_body_fields": "JSON fields the caller must supply before the API call.",
      "upstream": "Call the backend host of an environment allowed by the routing profile. Configure the environments and hosts in Config → Routing.",
      "upstream.port": "Optional port for the admitted upstream host. Omit it to use the scheme's default port.",
      "upstream.path": "Absolute path on the admitted upstream host, such as /v1/events/{event_id}. Do not include a query string or fragment.",
      "environment_variable": "Variable containing the selected environment, usually route. Its value must identify an environment allowed by the routing profile.",
      "method": "Fixed uppercase HTTP method; cannot accompany forward_method. Default POST.",
      "body_methods": "Only these methods carry a body. For example POST/PUT/PATCH, or add DELETE when required.",
      "query_mode": "all preserves repeated query values; first or last explicitly collapses them. Requires forward_query.",
      "remove_query": "Query names removed from the outbound copy, leaving ingress unchanged.",
      "header_mode": "forward copies approved inbound headers; replace starts with only configured outbound headers.",
      "remove_headers": "Inbound headers removed before configured headers are applied, e.g. the Hookway authentication token.",
      "remove_body_fields": "JSON dot paths removed from the outbound copy. Requires body: transformed and a JSON object.",
      "max_response_bytes": "Maximum upstream wire body (default 10 MiB, at most 64 MiB). Set 20971520 for a 20 MiB file budget.",
      "response_bindings": "Bounded non-sensitive response scalar paths and optional status variable. Selection failure preserves the upstream reply; scripts receive no raw response body or headers.",
      "max_json_bytes": "JSON response selection budget (default 64 KiB, maximum 1 MiB), separate from the relay body limit.",
      "redirects": "Optional max_hops (1..10). Redirects stay on the same scheme and authority and are revalidated; default returns redirects to the caller.",
      "on": "network retries transport/read failures only; network_and_status also retries 429/5xx. Creation routes should retain one attempt.",
      "methods": "Optional retry method allowlist. Methods outside it receive one attempt.",
      "total_timeout": "Deadline shared by all attempts and backoff, separate from each attempt's timeout.",
      "forward_method": "Send the original request method instead of POST.",
      "forward_query": "Copy the original query string onto the upstream URL.",
      "forward_headers": "Request header names to copy upstream.",
      "timeout": "Upstream deadline, e.g. 10s.",
      "headers": "Extra headers to send. Values may use ${vars}.",
      "error_on_status": "Status classes that fail this node, e.g. 5xx.",
      "body": "raw sends the original bytes; transformed sends the current event body.",
      "retry": "Optional: re-attempt transport errors, timeouts, 429, and 5xx. Leave empty for a single attempt.",
    },
    fieldLabels: {
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
    },
  },
  publish: {
    label: "Send to a destination",
    icon: TbSend,
    help: "Send the event or supported file content to a destination configured in this workspace. Connect multiple paths to the same input; each arriving payload is sent separately and runs its own success or error continuation.",
    editor: "publish",
    initialOptions: () => ({ create: true }),
    summary: (options) => [options.destination, options.target].filter(Boolean).map(String).join(" → "),
    fieldHelp: {
      "destination": "Id of an entry in Destinations.",
      "target": "Queue, topic, routing key, webhook path, or Lambda function name/ARN, depending on the selected destination.",
      "create": "Declare the queue/topic at startup when the backend supports it (not Lambda).",
      "message_attributes": "SNS/SQS message attributes (String, Number, String.Array, or Binary encoded as base64; SQS custom type suffixes supported).",
      "queue_owner_account_id": "Optional 12-digit account ID for an existing cross-account SQS queue; requires create: false and appropriate AWS permissions.",
      "topic_arn": "Explicit ARN for an existing SNS topic whose name equals target; requires create: false. Resolves directly without ListTopics.",
      "group_id": "Optional fair-queue group identity for standard SQS queues and SNS topics. FIFO targets use fifo.group_id.",
      "sqs": "SQS per-message options. delay_seconds (0..900) is valid only on standard queues.",
      "sns": "SNS per-message options: subject for standard topics; message_structure: json for protocol-specific messages with a string default field.",
      "delay_seconds": "Override the queue delay for this standard SQS message, from 0 to 900 seconds. FIFO delays belong in resource_attributes.",
      "subject": "Email subject on a standard SNS topic; fewer than 100 bytes without control characters.",
      "message_structure": "json sends protocol-specific messages. Wire body must be a JSON object with string values and a default key.",
      "resource_attributes": "AWS queue or topic settings, such as encryption and retention. Omitted settings are left unchanged. Nodes using the same target must agree; the conflict policy controls existing resources.",
      "wire_body": "SQS/SNS bytes: envelope (default), raw (original UTF-8), json (transformed JSON), or json_js (original request parsed and stringified with JavaScript semantics).",
      "fifo": "FIFO targets need a group ID and either an explicit deduplication ID or content-based deduplication. Choose where each ID comes from; replay preserves these IDs.",
      "source": "FIFO identity source: static template, body field, header, variable, SHA-256 of original bytes, or SHA-256 of original JSON parsed and stringified with JavaScript semantics.",
      "value": "Literal identity or {variable} template, used only with source: static.",
      "path": "Body dot path, header name or variable name for the selected source. Omit for static and hash sources.",
      "fallback": "Optional literal or {variable} template when the selected identity is missing. Cannot be combined with fallback_hash.",
      "fallback_hash": "Hash the original raw or JavaScript JSON representation when the selected body/header/variable identity is missing.",
      "hash_length": "Hex characters to retain for a hash source or fallback hash (1..64). Omit to keep all 64. This does not truncate an existing field identity.",
      "max_attempts": "Total broker attempts including the first (1..10). Required when an explicit retry policy is configured.",
      "initial_backoff": "Wait before the second broker attempt, doubled after each retry. Defaults to 100ms.",
      "max_backoff": "Maximum wait between broker attempts. Defaults to 1s; must be at least the initial backoff and at most 1m.",
      "retry": "Explicit broker attempts, including the first attempt (1..10). Backoffs default to 100ms and 1s. SDK retries are disabled within these attempts; server publish timeout bounds the total.",
      "retry.initial_backoff": "Wait before the second broker attempt. Defaults to 100ms.",
      "retry.max_backoff": "Maximum wait between broker attempts. Defaults to 1s.",
      "group_id.source": "Choose static, body, header, variable, or a supported request hash as the group identity source.",
      "group_id.value": "Identity template used when source is static. Supports {variable} substitutions.",
      "group_id.path": "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
      "fifo.group_id.source": "Choose static, body, header, variable, or a supported request hash as the FIFO group identity source.",
      "fifo.group_id.value": "Identity template used when source is static. Supports {variable} substitutions.",
      "fifo.group_id.path": "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
      "fifo.deduplication_id.source": "Choose static, body, header, variable, or a supported request hash as the deduplication identity source.",
      "fifo.deduplication_id.value": "Identity template used when source is static. Supports {variable} substitutions.",
      "fifo.deduplication_id.path": "For body, use a dotted body path; for header, enter the header name; for variable, enter the variable name.",
      "payload": "envelope sends the event as JSON. object sends raw file bytes and is supported only by S3 and Redis key/value destinations.",
    },
    fieldLabels: {
      target: "Delivery target",
      payload: "Payload to send",
      create: "Create the queue or topic at startup",
      wire_body: "Message body format",
      resource_attributes: "AWS queue or topic settings",
      message_attributes: "Message attributes",
    },
  },
  respond: {
    label: "Return an HTTP response",
    icon: TbArrowBackUp,
    help: "Return a fixed HTTP response or the last API response to the original caller.",
    editor: "respond",
    initialOptions: () => ({ passthrough: true }),
    summary: (options) => options.passthrough ? "passthrough" : String(options.status ?? ""),
    fieldHelp: {
      "status": "HTTP status to return. Omit if passthrough is on.",
      "headers": "Response headers to add.",
      "body": "Response body. Ignored when passthrough is on.",
      "forward_set_cookie": "Opt in to returning upstream Set-Cookie values. Disabled by default; review cookie domain/path for the public origin.",
      "forward_headers": "Optional allowlist of end-to-end response headers. Framing is computed by Hookway; Set-Cookie also requires forward_set_cookie.",
      "passthrough": "Return the last upstream http_request response instead of a fixed status/body.",
    },
    fieldLabels: {
      status: "HTTP status code",
      headers: "Response headers",
      body: "Response body",
      forward_headers: "API response headers to return",
      forward_set_cookie: "Return API response cookies",
    },
  },
};
export function nodeDescriptor(type: string): NodeDescriptor | undefined {
  return Object.prototype.hasOwnProperty.call(NODE_DESCRIPTORS, type)
    ? NODE_DESCRIPTORS[type as keyof typeof NODE_DESCRIPTORS]
    : undefined;
}
export function nodeLabel(type: string): string {
  return nodeDescriptor(type)?.label ?? humanLabel(type);
}
export function nodeSummary(type: string, options: Options): string {
  return nodeDescriptor(type)?.summary?.(options) ?? "";
}
export function labelledPorts(type: string, options: Options): string[] {
  return nodeDescriptor(type)?.labelledPorts?.(options) ?? [];
}
function switchPorts(options: Record<string, unknown>): string[] {
  const ports: string[] = [];
  const cases = options.cases;
  if (Array.isArray(cases)) {
    for (const c of cases) {
      if (c &&
        typeof c === "object" &&
        "port" in c &&
        String((c as {
          port?: unknown;
        }).port)) {
        ports.push(String((c as {
          port: unknown;
        }).port));
      }
    }
  }
  const fallback = String(options.default ?? "").trim();
  if (fallback && !ports.includes(fallback))
    ports.push(fallback);
  return ports;
}
