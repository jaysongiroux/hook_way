import { parse, parseDocument } from "yaml";
import { assertSchemaImportPrecision } from "./schemaImportPrecision";
import { restoreLiteralProvenance } from "./literalYaml";
import { fromConfig } from "./fromConfig";
import { layoutAllFlows } from "./layout";
import type { GraphDocument } from "./model";

export function fromYaml(text: string): GraphDocument {
  const config = parseYamlMapping(text, "Core config");
  if (config.flows && typeof config.flows === "object" && Object.values(config.flows).some((flow) => flow && typeof flow === "object" && Object.prototype.hasOwnProperty.call(flow, "file"))) {
    throw new Error("This entry YAML references flow files. Import the ZIP or select the entry and its flow files together.");
  }
  return restoreLiteralProvenance(layoutAllFlows(fromConfig(config)), text);
}

export function parseYamlMapping(text: string, label: string): Record<string, unknown> {
  const document = parseDocument(text, { intAsBigInt: true });
  if (document.errors.length) throw document.errors[0];
  assertSchemaImportPrecision(document);
  const parsed = parse(text);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(`${label} must be a YAML mapping.`);
  }
  return parsed as Record<string, unknown>;
}
