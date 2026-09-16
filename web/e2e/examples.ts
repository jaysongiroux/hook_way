import { readdirSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import { scanEnvVars } from "../src/graph/envScan";
import { NODE_IDENTITY_KEYS } from "../src/graph/nodeIdentity";

const examplesRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../examples");

export interface ExampleFixture {
  name: string;
  yaml: string;
  config: Record<string, unknown>;
  envVars: string[];
}

function listYaml(dir: string, include: (file: string) => boolean): string[] {
  return readdirSync(path.join(examplesRoot, dir))
    .filter((file) => file.endsWith(".yaml") && include(file))
    .sort()
    .map((file) => path.join(dir, file));
}

/** Valid Hookway configs from `examples/`, matching `make validate-examples`. */
export function exampleFixtures(): ExampleFixture[] {
  const rels = [
    "config.yaml",
    "filters.yaml",
    "replay.yaml",
    "variables.yaml",
    "on_error.yaml",
    ...listYaml("destinations", () => true),
    ...listYaml("auth", () => true),
    ...listYaml("showcase", (file) => !file.includes("invalid")),
  ];
  return rels.map((name) => {
    const yaml = readFileSync(path.join(examplesRoot, name), "utf8");
    const config = (parse(yaml) ?? {}) as Record<string, unknown>;
    return { name, yaml, config, envVars: scanEnvVars(JSON.stringify(config)) };
  });
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function flowNames(config: Record<string, unknown>): string[] {
  return isRecord(config.flows) ? Object.keys(config.flows) : [];
}

export function destinationNames(config: Record<string, unknown>): string[] {
  return isRecord(config.destinations) ? Object.keys(config.destinations) : [];
}

export interface FlowNodeYaml {
  id: string;
  type: string;
  description: string;
  options: Record<string, unknown>;
}


export function flowNodes(config: Record<string, unknown>, flow: string): FlowNodeYaml[] {
  const flows = isRecord(config.flows) ? config.flows : {};
  const raw = isRecord(flows[flow]) ? flows[flow] : {};
  const list = Array.isArray(raw.nodes) ? raw.nodes : [];
  return list.map((item, index) => {
    const rec = isRecord(item) ? item : {};
    const options: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(rec)) {
      if (!NODE_IDENTITY_KEYS.has(key)) options[key] = value;
    }
    return {
      id: String(rec.id ?? `node_${index + 1}`),
      type: String(rec.type ?? ""),
      description: rec.description === undefined ? "" : String(rec.description),
      options,
    };
  });
}

export function flowPolicy(config: Record<string, unknown>, flow: string): string {
  const flows = isRecord(config.flows) ? config.flows : {};
  const raw = isRecord(flows[flow]) ? flows[flow] : {};
  return String(raw.delivery_failure_policy ?? "").trim();
}

export function destEntry(config: Record<string, unknown>, name: string): { type: string; block: Record<string, unknown> } {
  const destinations = isRecord(config.destinations) ? config.destinations : {};
  const entry = isRecord(destinations[name]) ? destinations[name] : {};
  const type = String(entry.type ?? "");
  const block = isRecord(entry[type]) ? entry[type] : {};
  return { type, block };
}

export function serverConfig(config: Record<string, unknown>): Record<string, unknown> {
  return isRecord(config.server) ? config.server : {};
}

export const SERVER_LABELS: Record<string, string> = {
  addr: "Address",
  max_body_bytes: "Max body bytes",
  read_timeout: "Read timeout",
  write_timeout: "Write timeout",
  idle_timeout: "Idle timeout",
  shutdown_timeout: "Shutdown timeout",
  publish_timeout: "Publish timeout",
  max_inflight_requests: "Max inflight requests",
  fanout_concurrency: "Fan-out concurrency",
  readiness_interval: "Readiness interval",
  readiness_timeout: "Readiness timeout",
};

export function humanLabel(key: string): string {
  return key.replace(/_/g, " ");
}
