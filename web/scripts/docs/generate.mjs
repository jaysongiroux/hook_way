import {
  readFile,
  writeFile,
  mkdir,
  rm,
  copyFile,
  lstat,
  readdir,
  realpath,
} from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import remarkStringify from "remark-stringify";
import { visit } from "unist-util-visit";
import { toString } from "mdast-util-to-string";
import GithubSlugger from "github-slugger";
import { createCatalog, parser, repoRoot, under } from "./catalog.mjs";
import { compilePage } from "./render.mjs";

function headingIds(tree) {
  const slugger = new GithubSlugger();
  const result = new Set();
  visit(tree, "heading", (node) => result.add(slugger.slug(toString(node))));
  return result;
}
export function rewriteUrl(url, entry, catalog, assets) {
  if (url.startsWith("#")) return url;
  const github = `${catalog.config.repository}/blob/${catalog.config.ref}/`;
  let relative = url;
  let absoluteSource = false;
  if (url.startsWith(github)) {
    relative = url.slice(github.length);
    absoluteSource = true;
  } else if (/^(?:[a-z][a-z\d+.-]*:|\/\/|\/)/i.test(url)) return url;
  const parsed = new URL(relative, "https://source.invalid/");
  const pathname = decodeURIComponent(relative.split(/[?#]/)[0]);
  const source = path.posix
    .normalize(
      absoluteSource
        ? pathname
        : path.posix.join(path.posix.dirname(entry.source), pathname),
    )
    .replace(/\/$/, "");
  if (source === ".." || source.startsWith("../"))
    throw new Error(`${entry.source}: link escapes repository: ${url}`);
  const suffix = parsed.search + parsed.hash;
  if (!existsSync(path.join(catalog.root, source)))
    throw new Error(`${entry.source}: missing link target: ${url}`);
  if (catalog.links[source]) return catalog.links[source] + suffix;
  const trackedDirectory = [...catalog.tracked].some((file) =>
    file.startsWith(`${source}/`),
  );
  if (trackedDirectory && !catalog.links[source])
    return `${catalog.config.repository}/tree/${catalog.config.ref}/${source}${suffix}`;
  if (!catalog.tracked.has(source) && !catalog.available.has(source)) {
    throw new Error(`${entry.source}: missing or private link target: ${url}`);
  }
  if (
    catalog.config.trackedOnlyRoots.some((dir) => under(source, dir)) &&
    !catalog.tracked.has(source)
  ) {
    throw new Error(`${entry.source}: untracked documentation target: ${url}`);
  }
  // Excluded public source stays on GitHub; it is never copied to output.
  if (catalog.excluded(source) || !catalog.eligible(source))
    return github + source + suffix;
  if (
    /\.(?:sql|png|jpe?g|svg|gif|webp)$/i.test(source) ||
    path.posix.basename(source) === "Caddyfile"
  ) {
    assets.add(source);
    return `/docs/raw/${source}${suffix}`;
  }
  if (catalog.tracked.has(source)) return github + source + suffix;
  throw new Error(`${entry.source}: unsupported local link target: ${url}`);
}
export function renderMarkdown(entry, catalog, assets) {
  const tree = structuredClone(entry.tree);
  // Keep the original H1 anchor, with the visible title supplied by the marketing reader.
  const firstHeading = tree.children.findIndex(
    (node) => node.type === "heading" && node.depth === 1,
  );
  if (firstHeading >= 0) {
    const heading = tree.children[firstHeading];
    const slugger = new GithubSlugger();
    let anchor;
    visit(tree, "heading", (node) => {
      const id = slugger.slug(toString(node));
      if (node === heading) anchor = id;
    });
    tree.children[firstHeading] = {
      type: "html",
      value: `<span data-source-title="${encodeURIComponent(toString(heading))}" id="${anchor}"></span>`,
    };
  }
  visit(tree, (node) => {
    if (["link", "image", "definition"].includes(node.type))
      node.url = rewriteUrl(node.url, entry, catalog, assets);
  });
  return parser()
    .use(remarkStringify, { fences: true, bullet: "-" })
    .stringify(tree);
}
function sidebar(catalog) {
  function children(prefix) {
    const direct = catalog.entries.filter(
      (entry) =>
        entry.route.startsWith(prefix) &&
        entry.route !== prefix &&
        !entry.route.slice(prefix.length).replace(/\/$/, "").includes("/"),
    );
    return direct
      .sort((a, b) => a.title.localeCompare(b.title))
      .map((entry) => {
        const nested = children(entry.route);
        return nested.length
          ? {
              label: entry.title,
              collapsed: true,
              items: [{ label: "Overview", link: entry.route }, ...nested],
            }
          : { label: entry.title, link: entry.route };
      });
  }
  const groups = children("/docs/");
  groups.sort((a, b) => {
    const order = (label) => {
      const key = Object.entries(catalog.config.labels).find(
        ([, value]) => value === label,
      )?.[0];
      const index = catalog.config.sectionOrder.indexOf(key);
      return index < 0 ? -1 : index;
    };
    return order(a.label) - order(b.label);
  });
  return [{ label: "Documentation", link: "/docs/" }, ...groups];
}
export async function generate(root = repoRoot, { dryRun = false } = {}) {
  const catalog = await createCatalog(root);
  if (dryRun) return catalog;
  const generatedRoot = path.join(root, "web/.generated/docs");
  const docsRoot = path.join(generatedRoot, "markdown");
  const publicRoot = path.join(generatedRoot, "public");
  await rm(generatedRoot, { recursive: true, force: true });
  await mkdir(docsRoot, { recursive: true });
  await mkdir(publicRoot, { recursive: true });
  const assets = new Set();
  const pages = [];
  for (const entry of catalog.entries) {
    let body;
    if (entry.kind === "markdown") {
      body = renderMarkdown(entry, catalog, assets);
      if (/\/README\.md$/i.test(entry.source)) {
        const linked = [];
        visit(entry.tree, (node) => {
          if (["link", "definition"].includes(node.type))
            linked.push(
              rewriteUrl(node.url, entry, catalog, assets).split(/[?#]/)[0],
            );
        });
        const missing = catalog.entries.filter(
          (child) =>
            child.route.startsWith(entry.route) &&
            child.route !== entry.route &&
            !child.route
              .slice(entry.route.length)
              .replace(/\/$/, "")
              .includes("/") &&
            !linked.some((href) => href.startsWith(child.route)),
        );
        if (missing.length)
          body +=
            "\n## More in this section\n\n" +
            missing
              .sort((a, b) => a.title.localeCompare(b.title))
              .map((child) => `- [${child.title}](${child.route})`)
              .join("\n") +
            "\n";
      }
    } else if (entry.kind === "yaml") {
      const raw = `/docs/raw/${entry.source}`;
      // Longer fences prevent literal backticks in comments from closing a block.
      const fence = "`".repeat(
        Math.max(
          3,
          ...[...entry.text.matchAll(/`+/g)].map(
            (match) => match[0].length + 1,
          ),
        ),
      );
      const comments = entry.text
        .match(/^(?:#[^\n]*(?:\n|$))+/)?.[0]
        .replace(/^# ?/gm, "")
        .trim();
      body = `Source: \`${entry.source}\`\n\n${comments ? `${comments}\n\n` : ""}<a href="${raw}" download>Download YAML</a>\n\n<button type="button" data-copy-yaml="${raw}" aria-live="polite">Copy YAML</button>\n\n${fence}yaml title="${entry.source}"\n${entry.text.replace(/\n$/, "")}\n${fence}\n`;
      assets.add(entry.source);
    } else {
      const children = catalog.entries.filter(
        (child) =>
          child.route.startsWith(entry.route) &&
          child.route !== entry.route &&
          !child.route
            .slice(entry.route.length)
            .replace(/\/$/, "")
            .includes("/"),
      );
      body =
        children
          .sort((a, b) => a.title.localeCompare(b.title))
          .map((child) => `- [${child.title}](${child.route})`)
          .join("\n") + "\n";
    }
    const slug =
      entry.route.slice("/docs/".length).replace(/\/$/, "") || "index";
    const destination = path.join(docsRoot, `${slug}.md`);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, body);
    const page = await compilePage(entry, body, catalog.config);
    pages.push(page);
    const dataFile = path.join(publicRoot, page.dataUrl.slice("/docs/".length));
    await mkdir(path.dirname(dataFile), { recursive: true });
    await writeFile(dataFile, JSON.stringify(page));
  }
  // Check mapped fragments against source heading IDs before publishing.
  for (const entry of catalog.entries.filter(
    (entry) => entry.kind === "markdown",
  )) {
    visit(entry.tree, (node) => {
      if (!["link", "definition"].includes(node.type)) return;
      const url = rewriteUrl(node.url, entry, catalog, assets);
      if (!url.includes("#")) return;
      const [route, fragment] = url.split("#");
      const target = route ? catalog.routes.get(route.split("?")[0]) : entry;
      if (
        target?.tree &&
        !headingIds(target.tree).has(decodeURIComponent(fragment))
      ) {
        throw new Error(`${entry.source}: heading missing: ${node.url}`);
      }
    });
  }
  for (const source of assets) {
    const input = path.join(root, source);
    if ((await lstat(input)).isSymbolicLink())
      throw new Error(`Symlink asset: ${source}`);
    const sourceRoot = catalog.config.roots.find((directory) =>
      under(source, directory),
    );
    const resolvedRoot = await realpath(path.join(root, sourceRoot || "."));
    if (!(await realpath(input)).startsWith(`${resolvedRoot}/`))
      throw new Error(`Asset escapes publication root: ${source}`);
    const output = path.join(publicRoot, "raw", source);
    await mkdir(path.dirname(output), { recursive: true });
    await copyFile(input, output);
  }
  const manifest = {
    links: catalog.links,
    sidebar: sidebar(catalog),
    pages: pages.map(({ html, headings, ...page }) => page),
    assets: [...assets].sort(),
  };
  await writeFile(
    path.join(generatedRoot, "manifest.json"),
    JSON.stringify(manifest, null, 2) + "\n",
  );
  await writeFile(
    path.join(publicRoot, "search.json"),
    JSON.stringify(
      pages.map(({ route, title, searchText }) => ({
        route,
        title,
        text: searchText,
      })),
    ),
  );
  return catalog;
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const dryRun = process.argv.includes("--dry-run");
  const catalog = await generate(repoRoot, { dryRun });
  console.log(
    `Documentation: ${catalog.entries.filter((e) => e.source).length} source pages, ${catalog.omitted.length} excluded Markdown/YAML files.`,
  );
  if (dryRun) {
    for (const entry of catalog.entries)
      console.log(
        `${entry.route} <- ${entry.source ?? "(generated folder index)"}`,
      );
    for (const entry of catalog.omitted)
      console.log(`Excluded: ${entry.source} (${entry.reason})`);
  }
}
