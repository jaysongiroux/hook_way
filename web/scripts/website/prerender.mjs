import { readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import registry from "../../site.json" with { type: "json" };
import { metadataHtml, siteOrigin } from "../docs/render.mjs";
const webRoot = fileURLToPath(new URL("../../", import.meta.url));
const output = path.join(webRoot, "dist");
const template = await readFile(path.join(output, "index.html"), "utf8");
const manifest = JSON.parse(
  await readFile(path.join(output, ".vite/manifest.json"), "utf8"),
);
// ssrLoadModule resolves imported images to source URLs. Use their production
// manifest URLs so initial HTML matches the hydrated client.
function productionAssets(html) {
  for (const [source, asset] of Object.entries(manifest)) {
    if (source.startsWith("src/") && !asset.isEntry && !asset.isDynamicEntry)
      html = html.replaceAll("/" + source, "/" + asset.file);
  }
  return html;
}
const docs = JSON.parse(
  await readFile(path.join(webRoot, ".generated/docs/manifest.json"), "utf8"),
).pages;
for (const required of registry.requiredDocs) {
  if (!docs.some((page) => page.route === required))
    throw new Error(
      "Required launch page is not published: " +
        required +
        ". Documentation must be tracked by Git.",
    );
}
const server = await createServer({
  root: webRoot,
  mode: "production",
  server: { middlewareMode: true },
  appType: "custom",
});
const origin = siteOrigin();
try {
  const { renderPage } = await server.ssrLoadModule("/src/website/render.tsx");
  const pages = [
    ...registry.pages,
    {
      path: "/404/",
      title: "404 — Page not found",
      description: "This Hookway page could not be found.",
      indexable: false,
    },
  ];
  for (const page of pages) {
    const html = metadataHtml(
      template,
      { ...page, route: page.path, documentTitle: page.title },
      origin,
    ).replace(
      '<div id="root"></div>',
      '<div id="root" data-prerendered="true">' +
        productionAssets(renderPage(page.path)) +
        "</div>",
    );
    const target =
      page.path === "/404/"
        ? path.join(output, "404.html")
        : path.join(output, page.path, "index.html");
    await mkdir(path.dirname(target), { recursive: true });
    await writeFile(target, html);
  }
  await writeFile(
    path.join(output, "builder.html"),
    metadataHtml(
      template,
      {
        route: "/builder",
        title: "Hookway Config",
        documentTitle: "Hookway Config",
        description:
          "Build, validate and export Hookway configuration in your browser.",
        indexable: false,
      },
      origin,
    ),
  );
  const entries = [
    ...registry.pages.map((page) => ({
      route: page.path,
      title: page.title,
      description: page.description,
    })),
    ...docs,
  ];
  await writeFile(
    path.join(output, "sitemap.xml"),
    '<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">' +
      entries
        .map((page) => "<url><loc>" + origin + page.route + "</loc></url>")
        .join("") +
      "</urlset>",
  );
  await writeFile(
    path.join(output, "robots.txt"),
    "User-agent: *\nAllow: /\n\nSitemap: " + origin + "/sitemap.xml\n",
  );
  const selected = registry.llmsPaths.map((route) => {
    const page = entries.find((value) => value.route === route);
    if (!page) throw new Error("Missing AI discovery target: " + route);
    return (
      "- [" + page.title + "](" + origin + route + "): " + page.description
    );
  });
  await writeFile(
    path.join(output, "llms.txt"),
    "# Hookway\n\n> " +
      registry.description +
      "\n\nHookway is MIT-licensed. Operators run their own infrastructure. The public builder exports configuration; it does not deploy a server.\n\n## Product and documentation\n\n" +
      selected.join("\n") +
      "\n\n## Project\n\n- [Source and license](" +
      registry.github +
      "): maintained by Jason Giroux.\n",
  );
  console.log(
    "Prerendered " +
      registry.pages.length +
      " marketing pages; generated canonical sitemap, robots.txt, llms.txt and non-indexable builder/404 pages.",
  );
} finally {
  await server.close();
}
