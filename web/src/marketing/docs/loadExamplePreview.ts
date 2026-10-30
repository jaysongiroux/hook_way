import { stringify } from "yaml";
import { fromBundle, yamlPath } from "../../graph/bundle";
import { parseYamlMapping } from "../../graph/fromYaml";
import type { GraphDocument } from "../../graph/model";

export interface PublishedYAML {
  source: string | null;
  rawUrl: string | null;
}

const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);

/** Resolve only files in the docs publication, never arbitrary config URLs. */
export async function loadExamplePreview(
  page: PublishedYAML,
  publishedFiles: readonly PublishedYAML[],
  signal: AbortSignal,
): Promise<GraphDocument | null> {
  if (!page.source || !page.rawUrl) return null;
  const read = async (url: string) => {
    const response = await fetch(url, { signal });
    if (!response.ok) throw new Error("Example YAML unavailable");
    return response.text();
  };
  const text = await read(page.rawUrl);
  const config = parseYamlMapping(text, page.source);
  const slash = page.source.lastIndexOf("/");
  const directory = page.source.slice(0, slash + 1);
  const entry = yamlPath(page.source.slice(slash + 1));
  const files: Record<string, string> = Object.create(null);
  files[entry] = text;

  if (!("flows" in config)) {
    if (!Array.isArray(config.nodes)) return null;
    // Standalone flow pages have no entry document. Wrap the body in memory;
    // the displayed/downloaded source remains the original file.
    const name = entry.replace(/\.ya?ml$/, "");
    files[entry] = stringify({ flows: { [name]: config } });
  } else {
    if (!record(config.flows)) throw new Error("flows must be a mapping");
    const published = new Map(publishedFiles.filter((file) => file.source && file.rawUrl)
      .map((file) => [file.source, file.rawUrl!]));
    await Promise.all(Object.values(config.flows).map(async (flow) => {
      if (!record(flow) || !("file" in flow)) return;
      if (typeof flow.file !== "string") throw new Error("Invalid flow file reference");
      const relative = yamlPath(flow.file);
      const url = published.get(directory + relative);
      if (!url) throw new Error("Flow file is not published");
      files[relative] = await read(url);
    }));
  }

  const document = fromBundle({ entry, files }, null);
  return [document, ...Object.values(document.otherFlows)].some((flow) => flow.nodes.length)
    ? document : null;
}
