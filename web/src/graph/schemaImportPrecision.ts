import { isAlias, isMap, isScalar, isSeq, type Document } from "yaml";
import { precisionRisk } from "../shared/ui/jsonPrecision";

/** Reject a schema before yaml.toJS can round a numeric constraint. Other
 * configuration fields retain their existing parser behavior. Runtime YAML
 * supports exact numbers; this editor's schema objects use JavaScript numbers. */
export function assertSchemaImportPrecision(document: Document) {
  const visited = new Set<unknown>();
  const checked = new Set<unknown>();
  const check = (node: unknown) => {
    if (!node || checked.has(node)) return;
    checked.add(node);
    if (isAlias(node)) { check(node.resolve(document)); return; }
    if (isScalar(node) && node.type === "PLAIN" && typeof node.value === "string" &&
      node.tag !== "tag:yaml.org,2002:str" &&
      /^[+-]?(?:[0-9][0-9_]*(?:\.[0-9_]*)?|\.[0-9_]+)(?:[eE][+-]?[0-9_]+)?$/.test(node.source ?? "")) {
      throw new Error("A JSON Schema numeric literal cannot be represented safely by the builder's YAML parser. Use ordinary decimal notation or keep this configuration in YAML; the import was not changed.");
    }
    if (isScalar(node) && (typeof node.value === "number" || typeof node.value === "bigint")) {
      if (/^[+-]?0[0-9_]+$/.test(node.source ?? "") && /[1-9]/.test(node.source ?? "")) {
        throw new Error("A JSON Schema number uses ambiguous leading-zero notation. Use ordinary decimal notation; the import was not changed.");
      }
      let literal = typeof node.value === "bigint" ? String(node.value) : node.source ?? String(node.value);
      literal = literal.replace(/_/g, "").replace(/^\+/, "").replace(/^(-?)\./, (_match, sign: string) => `${sign}0.`).replace(/\.$/, ".0");
      const risk = precisionRisk(literal);
      if (risk || !Number.isFinite(Number(literal))) {
        throw new Error("A JSON Schema number would lose precision in the builder. Keep this configuration in YAML or replace the numeric constraint with an exactly representable value; the import was not changed.");
      }
    } else if (isMap(node)) {
      for (const pair of node.items) check(pair.value);
    } else if (isSeq(node)) {
      for (const item of node.items) check(item);
    }
  };
  const walk = (node: unknown) => {
    if (!node || visited.has(node)) return;
    visited.add(node);
    if (isAlias(node)) { walk(node.resolve(document)); return; }
    if (isMap(node)) {
      if (node.get("type") === "mcp_trigger") {
        const tools = node.get("tools", true);
        if (isSeq(tools)) for (const tool of tools.items) if (isMap(tool)) {
          check(tool.get("input_schema", true));
          check(tool.get("output_schema", true));
        }
      }
      if (node.get("type") === "json_validate") check(node.get("schema", true));
      if (node.get("type") === "transform" && node.get("kind") === "json_schema") check(node.get("json_schema", true));
      for (const pair of node.items) walk(pair.value);
    } else if (isSeq(node)) {
      for (const item of node.items) walk(item);
    }
  };
  if (isMap(document.contents)) {
    const events = document.contents.get("events", true);
    if (isMap(events)) for (const pair of events.items) {
      if (isMap(pair.value)) check(pair.value.get("payload_schema", true));
    }
  }
  walk(document.contents);
}
