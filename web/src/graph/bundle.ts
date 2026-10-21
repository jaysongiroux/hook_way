import { Unzip, UnzipInflate, strToU8, zip } from "fflate";
import { parseYamlMapping } from "./fromYaml";
import { fromConfig } from "./fromConfig";
import { layoutAllFlows } from "./layout";
import type { GraphDocument } from "./model";
import { toConfig } from "./toConfig";
import { literalRoots, restoreLiteralProvenance } from "./literalYaml";
import { toYaml } from "./toYaml";
import { fromReplayYaml, toReplayYaml } from "./replayYaml";

export interface YAMLBundle {
  entry: string;
  files: Record<string, string>;
}
const MAX_FILES = 1024;
export const MAX_DOCUMENT_BYTES = 5 * 1024 * 1024;
const MAX_BUNDLE_BYTES = 64 * 1024 * 1024;
const own = (object: object, key: string) =>
  Object.prototype.hasOwnProperty.call(object, key);
const record = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === "object" && !Array.isArray(value);
const directory = (filename: string) =>
  filename.includes("/")
    ? filename.slice(0, filename.lastIndexOf("/") + 1)
    : "";

function relativePath(filename: string): string {
  if (
    !filename ||
    filename.startsWith("/") ||
    /[\\:$*?\[\]\p{Cc}]/u.test(filename)
  )
    throw new Error(
      "Use a literal relative .yaml or .yml path inside the entry directory.",
    );
  const segments: string[] = [];
  for (const part of filename.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") {
      if (!segments.length)
        throw new Error("File paths cannot escape the entry directory.");
      segments.pop();
    } else segments.push(part);
  }
  const clean = segments.join("/");
  return clean;
}

export function yamlPath(filename: string): string {
  const clean = relativePath(filename);
  if (!/\.ya?ml$/.test(clean))
    throw new Error("File paths must end in .yaml or .yml.");
  return clean;
}

export function flowFilenames(doc: GraphDocument): Record<string, string> {
  const names = [doc.activeFlow, ...Object.keys(doc.otherFlows)].sort();
  const files = Object.fromEntries(
    names
      .filter((name) => own(doc.flowFiles ?? {}, name))
      .map((name) => [name, doc.flowFiles![name]]),
  );
  const taken = new Set(Object.values(files));
  for (const name of names) {
    if (own(files, name)) continue;
    const slug =
      name
        .normalize("NFKD")
        .replace(/[^a-zA-Z0-9_-]+/g, "_")
        .slice(0, 100) || "flow";
    let candidate = `flows/${slug}.yaml`;
    for (let i = 2; taken.has(candidate); i++)
      candidate = `flows/${slug}_${i}.yaml`;
    files[name] = candidate;
    taken.add(candidate);
  }
  return files;
}

export function toBundle(doc: GraphDocument): YAMLBundle {
  const entry = yamlPath(doc.entryFilename ?? "core.yaml");
  const files: Record<string, string> = Object.create(null);
  const { config } = toConfig(doc);
  if (doc.yamlMode === "split") {
    const names = flowFilenames(doc);
    const flows = config.flows as Record<string, unknown>;
    const references: Record<string, unknown> = Object.create(null);
    for (const [name, body] of Object.entries(flows).sort(([a], [b]) =>
      a.localeCompare(b),
    )) {
      const relative = yamlPath(names[name]);
      const filename = directory(entry) + relative;
      if (filename === entry || own(files, filename))
        throw new Error(`Duplicate YAML filename: ${filename}`);
      files[filename] = toYaml(body, literalRoots(doc, name));
      references[name] = { file: relative };
    }
    config.flows = references;
  }
  files[entry] = toYaml(config, literalRoots(doc));
  if (doc.replayServiceConfig !== null) {
    const replay = yamlPath(doc.replayFilename ?? "replay.yaml");
    if (own(files, replay))
      throw new Error(`Duplicate YAML filename: ${replay}`);
    files[replay] = toReplayYaml(doc.replayServiceConfig);
  }
  checkFiles(files);
  return { entry, files };
}

export function needsZip(doc: GraphDocument, bundle: YAMLBundle): boolean {
  return (
    Object.keys(doc.otherFlows).length > 0 ||
    Object.keys(bundle.files).length > 1
  );
}

export function checkFiles(
  input: Record<string, string>,
): Record<string, string> {
  if (Object.keys(input).length > MAX_FILES)
    throw new Error("Import exceeds 1,024 files.");
  const files: Record<string, string> = Object.create(null);
  let total = 0;
  for (const [filename, text] of Object.entries(input)) {
    const clean = yamlPath(filename);
    if (own(files, clean)) throw new Error(`Duplicate YAML filename: ${clean}`);
    const size = strToU8(text).byteLength;
    if (size > MAX_DOCUMENT_BYTES)
      throw new Error(`${filename} exceeds 5 MiB.`);
    total += size;
    if (total > MAX_BUNDLE_BYTES)
      throw new Error("Import exceeds 64 MiB of YAML.");
    files[clean] = text;
  }
  return files;
}

export function entryCandidates(files: Record<string, string>): string[] {
  return Object.keys(files).filter((filename) => {
    try {
      const config = parseYamlMapping(files[filename], filename);
      return (
        own(config, "flows") ||
        own(config, "destinations") ||
        own(config, "connections") ||
        (own(config, "server") &&
          !own(config, "database") &&
          !own(config, "core"))
      );
    } catch {
      return false;
    }
  });
}

export function missingFlowFiles(
  entry: string,
  files: Record<string, string>,
): string[] {
  const config = parseYamlMapping(files[entry] ?? "", entry);
  if (!record(config.flows)) return [];
  return Object.values(config.flows).flatMap((flow) => {
    if (!record(flow) || !own(flow, "file") || typeof flow.file !== "string")
      return [];
    const filename = directory(entry) + yamlPath(flow.file);
    return own(files, filename) ? [] : [filename];
  });
}

export function fromBundle(
  bundle: YAMLBundle,
  replayFilename?: string | null,
): GraphDocument {
  const files = checkFiles(bundle.files);
  const entry = yamlPath(bundle.entry);
  if (!own(files, entry)) throw new Error(`Missing entry YAML: ${entry}`);
  // Syntax-check every document before replacing any workspace state.
  for (const [name, text] of Object.entries(files)) {
    try {
      parseYamlMapping(text, name);
    } catch (error) {
      throw new Error(
        `${name}: ${error instanceof Error ? error.message : String(error)}`,
      );
    }
  }
  const config = parseYamlMapping(files[entry], entry);
  const flows = record(config.flows) ? { ...config.flows } : {};
  const flowFiles: Record<string, string> = Object.create(null);
  const used = new Set([entry]);
  let inline = false,
    split = false;
  for (const [name, value] of Object.entries(flows)) {
    if (!record(value) || !own(value, "file")) {
      inline = true;
      continue;
    }
    split = true;
    if (Object.keys(value).length !== 1 || typeof value.file !== "string")
      throw new Error(
        `flows.${name}: file references cannot contain inline fields.`,
      );
    const relative = yamlPath(value.file);
    const filename = directory(entry) + relative;
    if (used.has(filename))
      throw new Error(`Duplicate flow file reference: ${filename}`);
    if (!own(files, filename))
      throw new Error(`Missing flow file: ${filename}`);
    const body = parseYamlMapping(files[filename], filename);
    for (const key of [
      "file",
      "flows",
      "server",
      "security",
      "metrics",
      "logging",
      "replay",
      "connections",
      "destinations",
      "credentials",
      "lookups",
      "events",
      "routing",
      "state_stores",
    ]) {
      if (own(body, key))
        throw new Error(
          `${filename}: ${key} belongs in the entry file; flow files contain only a flow body.`,
        );
    }
    used.add(filename);
    flowFiles[name] = relative;
    flows[name] = body;
  }
  if (inline && split)
    throw new Error("Inline flows and file references cannot be mixed.");
  const doc = restoreLiteralProvenance(layoutAllFlows(fromConfig({ ...config, flows })), files[entry]);
  for (const [name, relative] of Object.entries(flowFiles)) {
    restoreLiteralProvenance(doc, files[directory(entry) + relative], name);
  }
  if (replayFilename === undefined) {
    const candidates = Object.keys(files).filter(
      (name) =>
        !used.has(name) &&
        own(parseYamlMapping(files[name], name), "database") &&
        own(parseYamlMapping(files[name], name), "core"),
    );
    if (candidates.length > 1)
      throw new Error(
        "Multiple replay configurations found. Select the replay YAML to import.",
      );
    replayFilename = candidates[0];
  }
  if (replayFilename) {
    replayFilename = yamlPath(replayFilename);
    if (!own(files, replayFilename) || used.has(replayFilename))
      throw new Error("Select a separate replay YAML file.");
    doc.replayServiceConfig = fromReplayYaml(files[replayFilename]);
    doc.replayFilename = replayFilename;
  }
  return {
    ...doc,
    entryFilename: entry,
    yamlMode: split ? "split" : "single",
    flowFiles,
  };
}

export function zipBundle(bundle: YAMLBundle): Promise<Uint8Array> {
  const files = checkFiles(bundle.files);
  return new Promise((resolve, reject) =>
    zip(
      Object.fromEntries(
        Object.entries(files).map(([name, text]) => [name, strToU8(text)]),
      ),
      { level: 6 },
      (error, data) => (error ? reject(error) : resolve(data)),
    ),
  );
}

// Stream entries and enforce actual decompressed sizes before retaining content.
export function unzipBundle(data: Uint8Array): Record<string, string> {
  if (data.length > 128 * 1024 * 1024) throw new Error("ZIP exceeds 128 MiB.");
  const files: Record<string, string> = Object.create(null);
  const seen = new Set<string>();
  let total = 0,
    count = 0;
  const archive = new Unzip((file) => {
    if (++count > MAX_FILES) throw new Error("ZIP exceeds 1,024 entries.");
    const name = file.name;
    // Directory entries are optional; validate their confinement as well.
    if (name.endsWith("/")) {
      yamlPath(name + "placeholder.yaml");
      return;
    }
    const canonical = relativePath(name);
    if (seen.has(canonical))
      throw new Error(`Duplicate ZIP path: ${canonical}`);
    seen.add(canonical);
    if (!/\.ya?ml$/.test(canonical)) return;
    if (
      file.originalSize !== undefined &&
      file.originalSize > MAX_DOCUMENT_BYTES
    )
      throw new Error(`${name} exceeds 5 MiB.`);
    const chunks: Uint8Array[] = [];
    let size = 0;
    file.ondata = (error, chunk, final) => {
      if (error) throw error;
      size += chunk.length;
      total += chunk.length;
      if (size > MAX_DOCUMENT_BYTES || total > MAX_BUNDLE_BYTES) {
        file.terminate();
        throw new Error(
          "ZIP exceeds YAML size limits (5 MiB per document, 64 MiB total).",
        );
      }
      chunks.push(chunk);
      if (final) {
        const bytes = new Uint8Array(size);
        let offset = 0;
        for (const part of chunks) {
          bytes.set(part, offset);
          offset += part.length;
        }
        files[canonical] = new TextDecoder("utf-8", { fatal: true }).decode(
          bytes,
        );
      }
    };
    file.start();
  });
  archive.register(UnzipInflate);
  for (let offset = 0; offset < data.length; offset += 4096)
    archive.push(
      data.subarray(offset, offset + 4096),
      offset + 4096 >= data.length,
    );
  if (!Object.keys(files).length)
    throw new Error("ZIP contains no YAML files.");
  return checkFiles(files);
}

export async function readYAMLFiles(
  selected: File[],
): Promise<Record<string, string>> {
  if (selected.length > MAX_FILES)
    throw new Error("Import exceeds 1,024 files.");
  if (selected.length === 1 && /\.zip$/i.test(selected[0].name)) {
    if (selected[0].size > 128 * 1024 * 1024)
      throw new Error("ZIP exceeds 128 MiB.");
    return unzipBundle(new Uint8Array(await selected[0].arrayBuffer()));
  }
  const files: Record<string, string> = Object.create(null);
  let total = 0;
  // Strip the selected directory's outer name, retaining paths underneath it.
  for (const file of selected) {
    const relative = file.webkitRelativePath;
    const filename = relative
      ? relative.slice(relative.indexOf("/") + 1)
      : file.name;
    if (!/\.ya?ml$/.test(filename)) continue;
    const canonical = yamlPath(filename);
    if (own(files, canonical))
      throw new Error(`Duplicate filename: ${canonical}`);
    if (file.size > MAX_DOCUMENT_BYTES)
      throw new Error(`${filename} exceeds 5 MiB.`);
    total += file.size;
    if (total > MAX_BUNDLE_BYTES)
      throw new Error("Import exceeds 64 MiB of YAML.");
    files[canonical] = await file.text();
  }
  return checkFiles(files);
}
