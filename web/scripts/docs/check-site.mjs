import assert from "node:assert/strict";
import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { parse } from "parse5";
import { repoRoot } from "./catalog.mjs";

export async function checkSite(root = repoRoot) {
  const manifest = JSON.parse(
    await readFile(
      path.join(root, "web/.generated/docs/manifest.json"),
      "utf8",
    ),
  );
  const output = path.join(root, "web/dist");
  const cache = new Map();
  async function document(file) {
    if (cache.has(file)) return cache.get(file);
    const tree = parse(await readFile(file, "utf8"));
    const ids = new Set();
    const urls = [];
    function walk(node) {
      const attributes = Object.fromEntries(
        (node.attrs || []).map(({ name, value }) => [name, value]),
      );
      if (attributes.id) ids.add(attributes.id);
      if (attributes.href) urls.push(attributes.href);
      if (attributes.src) urls.push(attributes.src);
      for (const child of node.childNodes || []) walk(child);
    }
    walk(tree);
    const result = { ids, urls };
    cache.set(file, result);
    return result;
  }
  for (const page of manifest.pages) {
    const file = path.join(output, page.route, "index.html");
    const { urls } = await document(file);
    for (const link of urls) {
      if (!link.startsWith("/docs/") && !link.startsWith("#")) continue;
      const url = new URL(link, `https://site.invalid${page.route}`);
      let target = path.join(output, decodeURIComponent(url.pathname));
      const info = await stat(target).catch(() => null);
      assert(info, `${page.route}: missing website target ${link}`);
      if (info.isDirectory()) target = path.join(target, "index.html");
      if (url.hash && target.endsWith(".html")) {
        const destination = await document(target);
        assert(
          destination.ids.has(decodeURIComponent(url.hash.slice(1))),
          `${page.route}: missing heading ${link}`,
        );
      }
    }
  }
  for (const source of manifest.assets) {
    assert.deepEqual(
      await readFile(path.join(output, "docs/raw", source)),
      await readFile(path.join(root, source)),
      `Changed download bytes: ${source}`,
    );
  }
  async function files(directory) {
    const result = [];
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name);
      result.push(...(entry.isDirectory() ? await files(file) : [file]));
    }
    return result;
  }
  const raw = await files(path.join(output, "docs/raw"));
  assert.deepEqual(
    raw
      .map((file) => path.relative(path.join(output, "docs/raw"), file))
      .sort(),
    [...manifest.assets].sort(),
    "Uncatalogued download files",
  );
  assert(
    (await stat(path.join(output, "docs/search.json"))).isFile(),
    "Search index missing",
  );
  console.log(
    `Verified ${manifest.pages.length} generated pages, internal links/anchors, ${manifest.assets.length} original downloads, and search output.`,
  );
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href)
  await checkSite();
