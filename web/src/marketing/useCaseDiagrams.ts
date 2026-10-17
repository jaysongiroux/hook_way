export interface ScenarioNode {
  id: string;
  label: string;
  detail: string;
  stage: number;
  lane: number;
}
export interface ScenarioDiagram {
  label: string;
  caption: string;
  nodes: ScenarioNode[];
  edges: [string, string][];
}
const node = (
  id: string,
  label: string,
  detail: string,
  stage: number,
  lane = 0.5,
): ScenarioNode => ({ id, label, detail, stage, lane });

export const SCENARIO_GROUPS = [
  {
    label: "Serve AI and API clients",
    items: [
      { id: "mcp-gateway", label: "APIs for AI assistants" },
      { id: "sync-proxy", label: "Synchronous API proxy" },
    ],
  },
  {
    label: "Route and deliver events",
    items: [
      { id: "fanout", label: "One event, multiple backends" },
      { id: "dynamic-routing", label: "Environment routing" },
      { id: "branching", label: "Conditional delivery" },
      { id: "redis-stream", label: "Redis Streams handoff" },
    ],
  },
  {
    label: "Work with the payload",
    items: [
      { id: "multipart-s3", label: "Files to S3, fields to a queue" },
      { id: "xml-proxy", label: "Clean and forward XML" },
      { id: "sampled-inspection", label: "Sample traffic for inspection" },
    ],
  },
];

// Simplified paths explain the pattern; the linked YAML is the full configuration.
export const SCENARIO_DIAGRAMS: Record<string, ScenarioDiagram> = {
  "mcp-gateway": {
    label:
      "An assistant calls an authenticated MCP endpoint, which runs its selected API or native tool branch and returns an MCP result.",
    caption:
      "One call runs one selected tool branch. Gateway and upstream credentials stay separate.",
    nodes: [
      node("client", "AI assistant", "Named tool call", 0),
      node("gateway", "MCP endpoint", "Authenticate · authorize", 1),
      node("api", "Status API", "get_service_status", 2, 0),
      node("native", "Native transform", "format_greeting", 2, 1),
      node("result", "MCP result", "Validated output", 3),
    ],
    edges: [
      ["client", "gateway"],
      ["gateway", "api"],
      ["gateway", "native"],
      ["api", "result"],
      ["native", "result"],
    ],
  },
  "sync-proxy": {
    label:
      "An HTTP caller passes authentication, calls the upstream API and receives its status and response body on the same connection.",
    caption:
      "The response returns to the original caller on the same connection.",
    nodes: [
      node("client", "HTTP caller", "Request", 0),
      node("auth", "Verify caller", "Inbound token", 1),
      node("api", "Upstream API", "HTTP request", 2),
      node("response", "Return response", "Status + body", 3),
    ],
    edges: [
      ["client", "auth"],
      ["auth", "api"],
      ["api", "response"],
    ],
  },
  fanout: {
    label:
      "A webhook is verified once and fans out to multiple publish destinations.",
    caption:
      "Declare destinations once, then reference them from separate publish branches.",
    nodes: [
      node("in", "Webhook", "Incoming event", 0),
      node("verify", "Verify & parse", "One envelope", 1),
      node("work", "Work queue", "Publish branch", 2, 0),
      node("archive", "Archive", "Publish branch", 2, 1),
    ],
    edges: [
      ["in", "verify"],
      ["verify", "work"],
      ["verify", "archive"],
    ],
  },
  "dynamic-routing": {
    label:
      "A requested environment is checked against the routing profile. Admitted events go to their environment's SQS queue; unresolved targets fail closed.",
    caption:
      "Only admitted environments reach a queue. Unknown or unresolved targets fail closed.",
    nodes: [
      node("in", "Webhook", "Requested environment", 0),
      node("route", "Routing profile", "Check allowed target", 1),
      node("queue", "Environment queue", "SQS", 2, 0),
      node("reject", "Reject request", "Unsafe or unresolved", 2, 1),
    ],
    edges: [
      ["in", "route"],
      ["route", "queue"],
      ["route", "reject"],
    ],
  },
  branching: {
    label:
      "Conditions on the parsed payload determine whether an event continues to delivery or its branch stops.",
    caption:
      "Filters stop unwanted events; if / else and switch select between delivery paths.",
    nodes: [
      node("in", "Parsed event", "Body + headers", 0),
      node("check", "Check conditions", "Payload fields", 1),
      node("send", "Deliver event", "Matching branch", 2, 0),
      node("stop", "Stop branch", "No match", 2, 1),
    ],
    edges: [
      ["in", "check"],
      ["check", "send"],
      ["check", "stop"],
    ],
  },
  "redis-stream": {
    label:
      "A verified webhook envelope is appended to a Redis or Valkey stream with XADD, then consumed by your workers with XREADGROUP.",
    caption:
      "Hookway appends events. Your workers own consumer groups and retention.",
    nodes: [
      node("in", "Webhook", "Verified envelope", 0),
      node("publish", "Publish", "XADD", 1),
      node("stream", "Redis / Valkey", "Stream", 2),
      node("worker", "Your workers", "XREADGROUP", 3),
    ],
    edges: [
      ["in", "publish"],
      ["publish", "stream"],
      ["stream", "worker"],
    ],
  },
  "multipart-s3": {
    label:
      "A multipart upload is verified and parsed, then decoded file bytes are stored in S3 while form fields and file metadata are published to RabbitMQ.",
    caption:
      "The S3 branch receives decoded file bytes. The queue receives fields and metadata.",
    nodes: [
      node("in", "File upload", "multipart/form-data", 0),
      node("parse", "Verify & parse", "Fields + files", 1),
      node("s3", "S3 object", "Decoded file bytes", 2, 0),
      node("queue", "RabbitMQ", "Fields + metadata", 2, 1),
    ],
    edges: [
      ["in", "parse"],
      ["parse", "s3"],
      ["parse", "queue"],
    ],
  },
  "xml-proxy": {
    label:
      "A signed XML webhook is verified, parsed and pruned, then forwarded to the upstream API.",
    caption:
      "Verify the raw request first. Forward the transformed document as XML.",
    nodes: [
      node("in", "XML webhook", "Verify signature", 0),
      node("parse", "Parse & prune", "Remove empty elements", 1),
      node("api", "Upstream API", "Transformed XML", 2),
      node("ack", "Respond", "202 accepted", 3),
    ],
    edges: [
      ["in", "parse"],
      ["parse", "api"],
      ["api", "ack"],
    ],
  },
  "sampled-inspection": {
    label:
      "Every webhook is published to the queue. A separate observation branch samples about five percent for view-only inspection.",
    caption:
      "The example samples about 5% for inspection. Publish still receives every event.",
    nodes: [
      node("in", "Webhook", "Incoming event", 0),
      node("env", "Parse event", "JSON envelope", 1),
      node("publish", "Publish all", "RabbitMQ", 2, 0),
      node("sample", "Sample", "Approximately 5%", 2, 1),
      node("inspect", "Inspect", "View-only capture", 3, 1),
    ],
    edges: [
      ["in", "env"],
      ["env", "publish"],
      ["env", "sample"],
      ["sample", "inspect"],
    ],
  },
};
