import { parse } from "yaml";
import { fromConfig } from "./fromConfig";
import { layout } from "./layout";
import type { GraphDocument } from "./model";

export function fromYaml(text: string): GraphDocument {
  const config = (parse(text) ?? {}) as Record<string, unknown>;
  const doc = fromConfig(config);
  return { ...doc, nodes: layout(doc.nodes, doc.edges) };
}
