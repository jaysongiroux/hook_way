import slackYaml from "../../../examples/templates/slack-notify.yaml?raw";
import discordYaml from "../../../examples/templates/discord-notify.yaml?raw";
import type { Edge } from "@xyflow/react";
import { fromYaml } from "./fromYaml";
import type { HookwayNode } from "./model";

/**
 * A slice of a template config ready to drop onto the canvas. Positions are
 * whatever the dagre layout produced for the source file; insertSubgraph
 * normalizes them to the drop point.
 */
export interface TemplateSubgraph {
  nodes: HookwayNode[];
  edges: Edge[];
}

/**
 * A palette template. `nodeIds` names the nodes to extract from `yaml` — the
 * rest of the file exists so the template is a complete config that
 * `make validate-examples` can check. See examples/README.md.
 */
export interface TemplateDef {
  id: string;
  label: string;
  help: string;
  nodeIds: string[];
  yaml: string;
}

export const TEMPLATES: TemplateDef[] = [
  {
    id: "slack-notify",
    label: "Slack notification",
    help: "Formats the event as a Slack message and posts it to an incoming webhook URL.",
    nodeIds: ["fmt", "post"],
    yaml: slackYaml,
  },
  {
    id: "discord-notify",
    label: "Discord notification",
    help: "Formats the event as a Discord message and posts it to a webhook URL.",
    nodeIds: ["fmt", "post"],
    yaml: discordYaml,
  },
];

const BY_ID = new Map(TEMPLATES.map((t) => [t.id, t]));

export function isTemplateId(s: string): boolean {
  return BY_ID.has(s);
}

/**
 * Extracts a template's named nodes and every edge between them from its
 * config. Returns undefined for an unknown id, and throws when the manifest
 * names an id the YAML does not define — a drifted manifest is a bug, not a
 * runtime condition, and templates.test.ts is where it should surface.
 */
export function templateSubgraph(id: string): TemplateSubgraph | undefined {
  const def = BY_ID.get(id);
  if (!def) return undefined;
  // fromYaml lays out the first flow as the active one (see fromConfig), which
  // is the flow every template file declares.
  const doc = fromYaml(def.yaml);
  const wanted = new Set(def.nodeIds);
  const nodes = doc.nodes.filter((n) => wanted.has(n.id));
  const missing = def.nodeIds.filter((want) => !nodes.some((n) => n.id === want));
  if (missing.length > 0) {
    throw new Error(`template ${def.id} names nodes missing from its config: ${missing.join(", ")}`);
  }
  const edges = doc.edges.filter((e) => wanted.has(e.source) && wanted.has(e.target));
  return { nodes, edges };
}
