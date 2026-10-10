import { execFileSync } from "node:child_process";
import { readFile, lstat, realpath } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { minimatch } from "minimatch";
import { parse } from "yaml";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import { toString } from "mdast-util-to-string";

export const repoRoot = fileURLToPath(new URL("../../../", import.meta.url));
export const parser = unified().use(remarkParse).use(remarkGfm);
export const under = (file, root) =>
  file === root || file.startsWith(`${root}/`);
export const humanize = (value) =>
  value.replace(/[-_]/g, " ").replace(/^./, (c) => c.toUpperCase());
export function sourceParts(text) {
  const match = text.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  return {
    data: match ? (parse(match[1]) ?? {}) : {},
    body: match ? text.slice(match[0].length) : text,
  };
}
export function routeFor(source) {
  if (source === "README.md") return "/docs/overview/";
  let route = source.replace(/^docs\//, "").replace(/\.(md|ya?ml)$/i, "");
  route = route.replace(/(^|\/)readme$/i, "").replace(/\/$/, "");
  return `/docs/${route ? `${route}/` : ""}`;
}
function relativePath(value) {
  if (
    typeof value !== "string" ||
    !value ||
    path.isAbsolute(value) ||
    value.includes("\\") ||
    value.split("/").includes("..")
  ) {
    throw new Error(
      `Publication paths must stay inside the repository: ${value}`,
    );
  }
}
export async function createCatalog(root = repoRoot) {
  const canonicalRoot = await realpath(root);
  const config = parse(
    await readFile(path.join(root, "web/docs-publication.yaml"), "utf8"),
  );
  for (const key of [
    "roots",
    "files",
    "include",
    "exclude",
    "trackedOnlyRoots",
  ]) {
    if (
      !Array.isArray(config[key]) ||
      config[key].some((v) => typeof v !== "string")
    )
      throw new Error(`Invalid publication ${key}`);
  }
  [
    ...config.roots,
    ...config.files,
    ...config.trackedOnlyRoots,
    ...config.exclude,
  ].forEach(relativePath);
  const tracked = new Set(
    execFileSync("git", ["ls-files", "-z"], { cwd: root, encoding: "utf8" })
      .split("\0")
      .filter(Boolean),
  );
  const excluded = (source) =>
    config.exclude.some((pattern) => minimatch(source, pattern, { dot: true }));
  const scoped = (source) =>
    config.files.includes(source) ||
    config.roots.some((dir) => under(source, dir));
  const eligible = (source) =>
    scoped(source) &&
    !excluded(source) &&
    (!config.trackedOnlyRoots.some((dir) => under(source, dir)) ||
      tracked.has(source));
  const candidates = [
    ...new Set(
      execFileSync(
        "git",
        [
          "ls-files",
          "-z",
          "--cached",
          "--others",
          ...(config.respectGitignore ? ["--exclude-standard"] : []),
          "--",
          ...config.roots,
          ...config.files,
        ],
        { cwd: root, encoding: "utf8" },
      )
        .split("\0")
        .filter(Boolean),
    ),
  ];
  const available = new Set(candidates);
  const entries = [];
  const omitted = [];
  for (const source of candidates.sort()) {
    if (!config.include.some((pattern) => minimatch(source, pattern))) continue;
    if (!eligible(source)) {
      omitted.push({
        source,
        reason: excluded(source) ? "excluded" : "not tracked by Git",
      });
      continue;
    }
    const info = await lstat(path.join(root, source)).catch((error) => {
      if (error.code === "ENOENT") return null;
      throw error;
    });
    if (!info) continue;
    if (info.isSymbolicLink())
      throw new Error(`Symlink cannot be published: ${source}`);
    const resolved = await realpath(path.join(root, source));
    const allowedRoot = config.roots.find((directory) =>
      under(source, directory),
    );
    if (
      allowedRoot &&
      !resolved.startsWith(`${path.join(canonicalRoot, allowedRoot)}/`)
    )
      throw new Error(`Source escapes publication root: ${source}`);
    const text = await readFile(path.join(root, source), "utf8");
    const kind = source.endsWith(".md") ? "markdown" : "yaml";
    const { data, body } =
      kind === "markdown" ? sourceParts(text) : { data: {}, body: text };
    const tree = kind === "markdown" ? parser.parse(body) : undefined;
    const heading = tree?.children.find(
      (node) => node.type === "heading" && node.depth === 1,
    );
    const title =
      data.title ||
      (heading && toString(heading)) ||
      humanize(path.basename(source).replace(/\.[^.]+$/, ""));
    entries.push({
      source,
      route: routeFor(source),
      title,
      kind,
      text,
      body,
      tree,
      description: data.description,
    });
  }
  const routes = new Map();
  const bySource = new Map();
  for (const entry of entries) {
    if (routes.has(entry.route))
      throw new Error(
        `Duplicate documentation route ${entry.route}: ${routes.get(entry.route).source} and ${entry.source}`,
      );
    routes.set(entry.route, entry);
    bySource.set(entry.source, entry.route);
    if (/\/README\.md$/i.test(entry.source))
      bySource.set(path.posix.dirname(entry.source), entry.route);
  }
  // Directory landing pages are automatic, even when no README exists.
  for (const entry of [...entries]) {
    let directory = path.posix.dirname(
      entry.route.slice("/docs/".length).replace(/\/$/, ""),
    );
    while (directory !== "." && directory !== "/") {
      const route = `/docs/${directory}/`;
      if (!routes.has(route)) {
        const title =
          config.labels[directory] || humanize(path.posix.basename(directory));
        const folder = { source: null, route, title, kind: "directory" };
        routes.set(route, folder);
        entries.push(folder);
        bySource.set(
          directory.startsWith("examples") ? directory : `docs/${directory}`,
          route,
        );
      }
      directory = path.posix.dirname(directory);
    }
  }
  const links = Object.fromEntries(bySource);
  return {
    root,
    config,
    tracked,
    excluded,
    eligible,
    available,
    entries,
    routes,
    links,
    omitted,
  };
}
