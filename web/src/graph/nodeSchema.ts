import nodeCatalog from "./nodeSchema.json";
import destCatalog from "./destinationSchema.json";

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
      default?: unknown;
    };

export interface NodeSchemaMeta {
  input: string;
  output: string;
  detached: boolean;
  schema: JSONSchema;
}

export interface DestSchemaMeta {
  block: string;
  schema: JSONSchema;
}

export const NODE_SCHEMAS = nodeCatalog as Record<string, NodeSchemaMeta>;
export const DEST_SCHEMAS = destCatalog as Record<string, DestSchemaMeta>;
export const NODE_TYPES = Object.keys(NODE_SCHEMAS).sort();
export const DEST_TYPES = Object.keys(DEST_SCHEMAS).sort();

export function isNodeType(s: string): s is string {
  return Object.prototype.hasOwnProperty.call(NODE_SCHEMAS, s);
}

export function nodeSchema(type: string): NodeSchemaMeta | undefined {
  return NODE_SCHEMAS[type];
}

export function destSchema(type: string): DestSchemaMeta | undefined {
  return DEST_SCHEMAS[type];
}

export function isSchemaObject(schema: JSONSchema | undefined): schema is Exclude<JSONSchema, boolean> {
  return typeof schema === "object" && schema !== null;
}

export function schemaProperties(schema: JSONSchema | undefined): Record<string, JSONSchema> {
  return isSchemaObject(schema) ? (schema.properties ?? {}) : {};
}

export function schemaRequired(schema: JSONSchema | undefined): string[] {
  return isSchemaObject(schema) ? (schema.required ?? []) : [];
}

export function schemaEnum(schema: JSONSchema | undefined): string[] | undefined {
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

export function nodeLabel(type: string): string {
  return humanLabel(type);
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
  auth: { scheme: "hmac_sha256_hex", signature_header: "X-Hub-Signature-256", secret: "${WEBHOOK_SECRET}" },
  respond: { passthrough: true },
  publish: { create: true },
  filter: { conditions: [{ source: "body", path: "", operator: "eq" }] },
  switch: { cases: [{ port: "matched", when: { source: "body", path: "", operator: "eq" } }], default: "default" },
};

export function defaultNodeOptions(type: string, id: string): Record<string, unknown> {
  const meta = NODE_SCHEMAS[type];
  const out: Record<string, unknown> = { ...(EXTRA_DEFAULTS[type] ?? {}) };
  if (type === "http_trigger" && out.path === undefined) out.path = `/webhooks/${id}`;
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

export function switchPorts(options: Record<string, unknown>): string[] {
  const ports: string[] = [];
  const cases = options.cases;
  if (Array.isArray(cases)) {
    for (const c of cases) {
      if (c && typeof c === "object" && "port" in c && String((c as { port?: unknown }).port)) {
        ports.push(String((c as { port: unknown }).port));
      }
    }
  }
  const fallback = String(options.default ?? "").trim();
  if (fallback && !ports.includes(fallback)) ports.push(fallback);
  return ports;
}
