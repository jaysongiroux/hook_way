import { parse } from "yaml";
import { fromConfig } from "./fromConfig";
import { layoutAllFlows } from "./layout";
import type { GraphDocument } from "./model";

export function fromYaml(text: string): GraphDocument {
  const config = parseYamlMapping(text, "Core config");
  return layoutAllFlows(fromConfig(config));
}

export function parseYamlMapping(text: string, label: string): Record<string, unknown> {
  const parsed = parse(text) ?? {};
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(`${label} must be a YAML mapping.`);
  }
  return parsed as Record<string, unknown>;
}
