import nodeCatalog from "./nodeSchema.json";
import destCatalog from "./destinationSchema.json";
import connCatalog from "./connectionSchema.json";

export type JSONSchema =
  | boolean
  | {
      type?: string;
      title?: string;
      description?: string;
      properties?: Record<string, JSONSchema>;
      required?: string[];
      enum?: unknown[];
      items?: JSONSchema;
      additionalProperties?: boolean | JSONSchema;
      oneOf?: JSONSchema[];
      not?: JSONSchema;
      minimum?: number;
      maximum?: number;
      minItems?: number;
      maxItems?: number;
      default?: unknown;
    };

export interface NodeSchemaMeta {
  input: string;
  output: string;
  detached: boolean;
  on_error: boolean;
  schema: JSONSchema;
  auth_schemes?: Record<
    string,
    {
      request_signature: boolean;
      separate_timestamp: boolean;
      jwt: boolean;
      key_format: "shared_secret" | "pem" | "base64_der";
    }
  >;
}

export interface DestSchemaMeta {
  block: string;
  /**
   * The connection type that can serve this destination, absent for a
   * destination with no connection form. Generated from
   * connTypeForDestType in internal/config, so the pairing cannot drift out
   * of sync with what the loader accepts.
   */
  connection?: string;
  schema: JSONSchema;
}

export const NODE_SCHEMAS = nodeCatalog as Record<string, NodeSchemaMeta>;
export const DEST_SCHEMAS = destCatalog as Record<string, DestSchemaMeta>;
// Unlike a destination, a connection is authored flat -- `type:` sits
// alongside its fields rather than above a per-type block -- so there is no
// block name to carry and the schema is the whole entry.
const CONN_SCHEMAS = connCatalog as Record<string, JSONSchema>;
export const NODE_TYPES = Object.keys(NODE_SCHEMAS).sort();
export const DEST_TYPES = Object.keys(DEST_SCHEMAS).sort();
export const CONN_TYPES = Object.keys(CONN_SCHEMAS).sort();

export function isNodeType(s: string): s is string {
  return Object.prototype.hasOwnProperty.call(NODE_SCHEMAS, s);
}

/** Whether the builder should draw an on_error handle for this catalog type. */
export function nodeHasOnError(type: string): boolean {
  return NODE_SCHEMAS[type]?.on_error === true;
}

export function destSchema(type: string): DestSchemaMeta | undefined {
  return DEST_SCHEMAS[type];
}

export function connSchema(type: string): JSONSchema | undefined {
  return CONN_SCHEMAS[type];
}

/**
 * The connection type that can serve a destination type, read from the
 * generated catalog. The four AWS backends share one credential shape, as do
 * the four redis variants; a webhook has none, because its credential carries
 * no host and is therefore an auth profile rather than a connection.
 */
export function connTypeForDest(destType: string): string | undefined {
  return DEST_SCHEMAS[destType]?.connection;
}

/**
 * The dotted field paths a connection of this type owns. A destination that
 * names a connection must not set these, so the form hides them rather than
 * letting the operator author config the Go validator rejects.
 */
export function connOwnedPaths(connType: string): string[] {
  const schema = connSchema(connType);
  const out: string[] = [];
  const walk = (s: JSONSchema | undefined, prefix: string) => {
    for (const [key, child] of Object.entries(schemaProperties(s))) {
      const path = prefix ? `${prefix}.${key}` : key;
      out.push(path);
      if (Object.keys(schemaProperties(child)).length > 0) walk(child, path);
    }
  };
  walk(schema, "");
  return out;
}

export function isSchemaObject(
  schema: JSONSchema | undefined,
): schema is Exclude<JSONSchema, boolean> {
  return typeof schema === "object" && schema !== null;
}

export function schemaProperties(
  schema: JSONSchema | undefined,
): Record<string, JSONSchema> {
  return isSchemaObject(schema) ? (schema.properties ?? {}) : {};
}

export function schemaRequired(schema: JSONSchema | undefined): string[] {
  return isSchemaObject(schema) ? (schema.required ?? []) : [];
}

export function schemaEnum(
  schema: JSONSchema | undefined,
): string[] | undefined {
  if (!isSchemaObject(schema) || !schema.enum) return undefined;
  return schema.enum.map((v) => String(v));
}

export function schemaType(schema: JSONSchema | undefined): string {
  if (!isSchemaObject(schema)) return schema === true ? "any" : "";
  if (schema.enum) return "enum";
  if (schema.type) return schema.type;
  if (schema.properties || schema.additionalProperties) return "object";
  if (schema.items) return "array";
  return "any";
}

export function humanLabel(key: string): string {
  return key.replace(/_/g, " ");
}

const NODE_LABELS: Record<string, string> = {
  http_trigger: "HTTP endpoint",
  loop_guard: "Prevent request loops",
  ip_allowlist: "Allow client IPs",
  rate_limit: "Limit request rate",
  read_body: "Read request body",
  auth: "Verify signature or token",
  no_auth: "Skip authentication",
  replay_guard: "Reject duplicate requests",
  envelope: "Parse request body",
  filter: "Filter events",
  register_route: "Save a route",
  route: "Select an environment",
  lookup: "Look up a value",
  batch: "Process a batch",
  transform: "Transform data",
  code: "Run JavaScript",
  switch: "Switch between routes",
  if_else: "Branch with if / else",
  http_request: "Call an HTTP API",
  publish: "Send to a destination",
  respond: "Return an HTTP response",
};

export function nodeLabel(type: string): string {
  return NODE_LABELS[type] ?? humanLabel(type);
}

/** Strict equality, matching graph.PortType.Accepts. */
export function portAccepts(input: string, output: string): boolean {
  return input === output;
}

const EXTRA_DEFAULTS: Record<string, Record<string, unknown>> = {
  http_trigger: { methods: ["POST"] },
  read_body: { max_bytes: 1048576 },
  rate_limit: { rpm: 60 },
  envelope: { body_format: "json" },
  auth: {
    scheme: "hmac_sha256_hex",
    source: { kind: "header", key: "X-Signature" },
    secret_ref: { provider: "env", name: "WEBHOOK_SECRET" },
  },
  respond: { passthrough: true },
  publish: { create: true },
  filter: { conditions: [{ source: "body", path: "", operator: "eq" }] },
  switch: {
    cases: [
      { port: "matched", when: { source: "body", path: "", operator: "eq" } },
    ],
    default: "default",
  },
  if_else: {
    match: "all",
    conditions: [{ source: "body", path: "", operator: "eq" }],
  },
};

export function defaultNodeOptions(
  type: string,
  id: string,
): Record<string, unknown> {
  const meta = NODE_SCHEMAS[type];
  const out: Record<string, unknown> = { ...(EXTRA_DEFAULTS[type] ?? {}) };
  if (type === "http_trigger" && out.path === undefined)
    out.path = `/webhooks/${id}`;
  for (const key of schemaRequired(meta?.schema)) {
    if (out[key] !== undefined) continue;
    out[key] = defaultForSchema(schemaProperties(meta?.schema)[key]);
  }
  return out;
}

export function defaultForSchema(schema: JSONSchema | undefined): unknown {
  if (!isSchemaObject(schema)) return "";
  if (schema.default !== undefined) return schema.default;
  const enums = schemaEnum(schema);
  if (enums && enums.length > 0) return enums[0];
  switch (schemaType(schema)) {
    case "boolean":
      return false;
    case "number":
    case "integer":
      return "";
    case "array":
      return [];
    case "object":
      return {};
    default:
      return "";
  }
}

function switchPorts(options: Record<string, unknown>): string[] {
  const ports: string[] = [];
  const cases = options.cases;
  if (Array.isArray(cases)) {
    for (const c of cases) {
      if (
        c &&
        typeof c === "object" &&
        "port" in c &&
        String((c as { port?: unknown }).port)
      ) {
        ports.push(String((c as { port: unknown }).port));
      }
    }
  }
  const fallback = String(options.default ?? "").trim();
  if (fallback && !ports.includes(fallback)) ports.push(fallback);
  return ports;
}

/**
 * The labelled output ports a node exposes, mirroring graph.PortDeclarer in Go.
 * An empty result means the node has the single default out handle instead.
 * Both the canvas handles and connection validation read this, so a new
 * branching node type is added here and nowhere else.
 */
export function labelledPorts(
  nodeType: string,
  options: Record<string, unknown>,
): string[] {
  if (nodeType === "if_else") return ["true", "false"];
  if (nodeType === "switch") return switchPorts(options);
  return [];
}
