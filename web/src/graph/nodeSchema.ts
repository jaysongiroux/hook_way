import { nodeDescriptor } from "./nodeDescriptors";
export { nodeLabel, labelledPorts } from "./nodeDescriptors";
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
  /** Positive values permit batch children and define the authoring order. */
  batch_child_order?: number;
  schema: JSONSchema;
  stage_ports?: Record<string, { input: string; output: string }>;
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
/** Effective typed ports come from the generated Go contract. */
export function nodeMeta(type: string, options: Record<string, unknown>): NodeSchemaMeta | undefined {
  const meta = NODE_SCHEMAS[type];
  const ports = meta?.stage_ports?.[String(options.stage ?? "request_head")];
  return ports ? { ...meta, ...ports } : meta;
}

export const DEST_SCHEMAS = destCatalog as Record<string, DestSchemaMeta>;
// Unlike a destination, a connection is authored flat -- `type:` sits
// alongside its fields rather than above a per-type block -- so there is no
// block name to carry and the schema is the whole entry.
const CONN_SCHEMAS = connCatalog as Record<string, JSONSchema>;
export const NODE_TYPES = Object.keys(NODE_SCHEMAS).sort();
export const BATCH_CHILD_TYPES = NODE_TYPES.filter(
  (type) => (NODE_SCHEMAS[type].batch_child_order ?? 0) > 0,
).sort(
  (a, b) => NODE_SCHEMAS[a].batch_child_order! - NODE_SCHEMAS[b].batch_child_order!,
);
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

export { humanLabel } from "../shared/ui/text";

/** Strict equality, matching graph.PortType.Accepts. */
export function portAccepts(input: string, output: string): boolean {
  return input === output;
}

export function defaultNodeOptions(
  type: string,
  id: string,
): Record<string, unknown> {
  const meta = NODE_SCHEMAS[type];
  const out = nodeDescriptor(type)?.initialOptions?.(id) ?? {};
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
