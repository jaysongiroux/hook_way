import { parseYamlMapping } from "./fromYaml";
import { yamlPath, type YAMLBundle } from "./bundle";

interface ExampleFlow {
  name: string;
  nodeCount: number;
  edgeCount: number;
}

export interface RepositoryExample extends YAMLBundle {
  /** Path relative to examples/, independent of the portable bundle filenames. */
  id: string;
  label: string;
  flows: ExampleFlow[];
}

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);

function excluded(path: string): boolean {
  return /(^|\/)invalid[-_.]/i.test(path) ||
    /(^|\/)(docker-)?compose[^/]*\.ya?ml$/i.test(path) ||
    /(^|\/)monitoring\//.test(path) ||
    /^mcp-events\/demo\/(runtime|evidence)\//.test(path);
}

/** Shared by the Vite glob and catalogue verification; never lays out graphs. */
export function createExampleCatalog(input: Record<string, string>): RepositoryExample[] {
  const examples: RepositoryExample[] = [];
  for (const id of Object.keys(input).sort()) {
    if (!/\.ya?ml$/.test(id) || excluded(id)) continue;
    try {
      const config = parseYamlMapping(input[id], id);
      // Non-core YAML and standalone flow bodies are not importable examples.
      if (!("flows" in config) || "openapi" in config || "services" in config ||
          ("database" in config && "core" in config)) continue;
      if (!record(config.flows)) throw new Error("flows must be a mapping.");
      const slash = id.lastIndexOf("/");
      const directory = id.slice(0, slash + 1);
      const entry = yamlPath(id.slice(slash + 1));
      const files: Record<string, string> = { [entry]: input[id] };
      const flows: ExampleFlow[] = [];
      let split = false, inline = false;
      for (const [name, value] of Object.entries(config.flows)) {
        if (!record(value)) throw new Error(`flows.${name} must be a mapping.`);
        let body = value;
        if ("file" in value) {
          split = true;
          if (Object.keys(value).length !== 1 || typeof value.file !== "string")
            throw new Error(`flows.${name}: invalid flow file reference.`);
          const relative = yamlPath(value.file);
          const source = directory + relative;
          if (!(source in input)) throw new Error(`Missing flow file: ${source}`);
          if (relative in files) throw new Error(`Duplicate flow file: ${relative}`);
          files[relative] = input[source];
          body = parseYamlMapping(input[source], source);
        } else inline = true;
        if (!Array.isArray(body.nodes) || (body.edges !== undefined && !Array.isArray(body.edges)))
          throw new Error(`flows.${name}: nodes and edges must be arrays.`);
        flows.push({ name, nodeCount: body.nodes.length, edgeCount: Array.isArray(body.edges) ? body.edges.length : 0 });
      }
      if (split && inline) throw new Error("Inline flows and file references cannot be mixed.");
      if (!flows.some((flow) => flow.nodeCount > 0)) continue;
      const stem = entry.replace(/\.ya?ml$/, "").replace(/[-_]+/g, " ");
      examples.push({
        id, entry, files, flows,
        label: stem.charAt(0).toUpperCase() + stem.slice(1),
      });
    } catch (error) {
      throw new Error(`${id}: ${error instanceof Error ? error.message : String(error)}`);
    }
  }
  return examples;
}
