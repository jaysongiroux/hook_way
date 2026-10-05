import { parseYamlMapping } from "./fromYaml";
import { toYaml } from "./toYaml";

export function fromReplayYaml(text: string): Record<string, unknown> {
  return parseYamlMapping(text, "Replay config");
}

export function toReplayYaml(config: Record<string, unknown>): string {
  const { console: _removedConsoleSetting, ...serviceConfig } = config;
  return toYaml(pruneUndefined(serviceConfig) as Record<string, unknown>);
}

function pruneUndefined(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(pruneUndefined);
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .filter(([, child]) => child !== undefined)
        .map(([key, child]) => [key, pruneUndefined(child)]),
    );
  }
  return value;
}
