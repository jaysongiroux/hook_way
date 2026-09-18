import slackYaml from "../../../examples/templates/slack-notify.yaml?raw";
import discordYaml from "../../../examples/templates/discord-notify.yaml?raw";
import stripeYaml from "../../../examples/templates/stripe-ingest.yaml?raw";
import paddleYaml from "../../../examples/templates/paddle-ingest.yaml?raw";
import xmlYaml from "../../../examples/templates/xml-ingest.yaml?raw";
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
    id: "stripe-ingest",
    label: "Stripe webhook",
    help: "Verifies Stripe-Signature with a rotating secret, sheds loops and bursts, drops retries already seen, and builds the JSON envelope.",
    nodeIds: ["in", "hops", "rl", "body", "auth", "replay", "env"],
    yaml: stripeYaml,
  },
  {
    id: "paddle-ingest",
    label: "Paddle webhook",
    help: "Verifies Paddle-Signature with a rotating secret, sheds loops and bursts, drops retries already seen, and builds the JSON envelope.",
    nodeIds: ["in", "hops", "rl", "body", "auth", "replay", "env"],
    yaml: paddleYaml,
  },
  {
    id: "xml-ingest",
    label: "XML webhook",
    help: "Verifies an HMAC over the raw bytes, parses the body into an XML element tree, filters on an element path, reads fields into vars, prunes empty elements, and forwards the document as application/xml. Inserts the whole pipeline because xml_prune only validates behind an xml envelope.",
    nodeIds: ["in", "hops", "rl", "body", "auth", "replay", "env", "only_loans", "tag", "prune", "post"],
    yaml: xmlYaml,
  },
  {
    id: "slack-notify",
    label: "Slack notification",
    help: "Formats the event as a Slack message and posts it to an env-held incoming-webhook URL. 4xx is terminal; caller credentials are not forwarded.",
    nodeIds: ["fmt", "post"],
    yaml: slackYaml,
  },
  {
    id: "discord-notify",
    label: "Discord notification",
    help: "Formats the event as a Discord message and posts it to an env-held webhook URL. 4xx is terminal; caller credentials are not forwarded.",
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
