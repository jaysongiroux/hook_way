import assert from "node:assert/strict";
import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { parse } from "parse5";
import { repoRoot } from "./catalog.mjs";
import registry from "../../site.json" with { type: "json" };
import { siteOrigin, siteIndexable } from "./render.mjs";

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
    const metadata = {
      h1: 0,
      title: "",
      description: "",
      canonical: "",
      robots: "",
    };
    function walk(node) {
      const attributes = Object.fromEntries(
        (node.attrs || []).map(({ name, value }) => [name, value]),
      );
      if (attributes.id) ids.add(attributes.id);
      if (node.tagName === "h1") metadata.h1++;
      if (node.tagName === "title")
        metadata.title = (node.childNodes || [])
          .map((child) => child.value || "")
          .join("");
      if (node.tagName === "meta" && attributes.name === "description")
        metadata.description = attributes.content;
      if (node.tagName === "meta" && attributes.name === "robots")
        metadata.robots = attributes.content;
      if (node.tagName === "link" && attributes.rel === "canonical")
        metadata.canonical = attributes.href;
      if (
        node.tagName === "script" &&
        attributes.type === "application/ld+json"
      )
        JSON.parse(
          (node.childNodes || []).map((child) => child.value || "").join(""),
        );
      if (attributes.href) urls.push(attributes.href);
      if (attributes.src) urls.push(attributes.src);
      for (const child of node.childNodes || []) walk(child);
    }
    walk(tree);
    const result = { ids, urls, metadata };
    cache.set(file, result);
    return result;
  }
  const titles = new Set();
  const sitePages = [
    ...manifest.pages,
    ...registry.pages.map((page) => ({ route: page.path })),
  ];
  for (const required of registry.requiredDocs)
    assert(
      manifest.pages.some((page) => page.route === required),
      "Missing tracked launch page: " + required,
    );
  for (const page of sitePages) {
    const file = path.join(output, page.route, "index.html");
    const { urls, metadata } = await document(file);
    assert.equal(
      metadata.h1,
      1,
      page.route + ": one H1 must exist in initial HTML",
    );
    assert(metadata.description, page.route + ": missing description");
    assert(
      !titles.has(metadata.title),
      page.route + ": duplicate document title " + metadata.title,
    );
    titles.add(metadata.title);
    assert.equal(
      metadata.canonical,
      siteOrigin() + page.route,
      page.route + ": canonical mismatch",
    );
    assert.equal(
      metadata.robots,
      siteIndexable() ? "index, follow" : "noindex, follow",
      page.route + ": wrong robots policy",
    );
    for (const link of urls) {
      if (!link.startsWith("/") && !link.startsWith("#")) continue;
      if (link === "/builder") continue;
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
  assert(
    (await stat(path.join(output, registry.image))).isFile(),
    "Missing social preview image",
  );
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
