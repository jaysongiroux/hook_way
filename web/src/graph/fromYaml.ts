import { parse } from "yaml";
import { fromConfig } from "./fromConfig";
import { layoutAllFlows } from "./layout";
import type { GraphDocument } from "./model";

export function fromYaml(text: string): GraphDocument {
  const config = (parse(text) ?? {}) as Record<string, unknown>;
  return layoutAllFlows(fromConfig(config));
}
