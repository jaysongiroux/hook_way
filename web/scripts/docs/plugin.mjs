import { readFile, readdir, stat } from "node:fs/promises";
import path from "node:path";
import { parse } from "parse5";
import { execFileSync } from "node:child_process";
import { createCatalog, repoRoot } from "./catalog.mjs";
import { generate } from "./generate.mjs";
import { pageHtml, siteIndexable, siteOrigin } from "./render.mjs";

const ids = ["virtual:hookway-docs", "virtual:hookway-docs-data"];
export function documentationPlugin(mode, origin) {
  let manifest;
  let pending;
  const generated = path.join(repoRoot, "web/.generated/docs");
  async function publication() {
    if (manifest) return manifest;
    pending ??= (async () => {
      await generate();
      manifest = JSON.parse(
        await readFile(path.join(generated, "manifest.json"), "utf8"),
      );
      return manifest;
    })().finally(() => {
      pending = undefined;
    });
    return pending;
  }
  return {
    name: "marketing-documentation",
    enforce: "post",
    resolveId(id) {
      if (ids.includes(id)) return `\0${id}`;
    },
    async load(id) {
      if (id === "\0virtual:hookway-docs") {
        const html = parse(
          await readFile(path.join(repoRoot, "web/index.html"), "utf8"),
        );
        let description = "";
        function walk(node) {
          const attributes = Object.fromEntries(
            (node.attrs || []).map(({ name, value }) => [name, value]),
          );
          if (node.tagName === "meta" && attributes.name === "description")
            description = attributes.content;
          for (const child of node.childNodes || []) walk(child);
        }
        walk(html);
        const site = {
          description,
          origin: siteOrigin(origin),
          indexable: siteIndexable(),
        };
        return `export const site = ${JSON.stringify(site)}; export default ${JSON.stringify((await createCatalog()).links)};`;
      }
      if (id === "\0virtual:hookway-docs-data") {
        const data = await publication();
        return `export default ${JSON.stringify({ sidebar: data.sidebar, pages: data.pages.map(({ searchText, ...page }) => page), origin: siteOrigin(origin) })};`;
      }
    },
    configureServer(server) {
      if (mode === "console") return;
      const gitDirectory = execFileSync(
        "git",
        ["rev-parse", "--absolute-git-dir"],
        { cwd: repoRoot, encoding: "utf8" },
      ).trim();
      const files = [
        "docs",
        "examples",
        "README.md",
        ".gitignore",
        "web/docs-publication.yaml",
      ].map((file) => path.join(repoRoot, file));
      files.push(path.join(gitDirectory, "index"));
      server.watcher.add(files);
      let debounce;
      server.watcher.on("all", (_event, file) => {
        if (
          !files.some(
            (source) => file === source || file.startsWith(`${source}/`),
          )
        )
          return;
        clearTimeout(debounce);
        debounce = setTimeout(async () => {
          // Wait for an in-flight generation before invalidating its result.
          await pending?.catch(() => {});
          manifest = undefined;
          for (const id of ids) {
            const module = server.moduleGraph.getModuleById(`\0${id}`);
            if (module) server.moduleGraph.invalidateModule(module);
          }
          server.ws.send({ type: "full-reload" });
        }, 200);
      });
      server.httpServer?.once("close", () => clearTimeout(debounce));
      server.middlewares.use(async (request, response, next) => {
        const pathname = new URL(request.url, "http://localhost").pathname;
        if (!/^\/docs\/(?:data\/|raw\/|search\.json$)/.test(pathname))
          return next();
        try {
          await publication();
          const root = path.join(generated, "public");
          const file = path.resolve(
            root,
            decodeURIComponent(pathname.slice("/docs/".length)),
          );
          if (!file.startsWith(`${root}/`) || !(await stat(file)).isFile())
            return next();
          response.setHeader(
            "Content-Type",
            {
              ".json": "application/json",
              ".svg": "image/svg+xml",
              ".png": "image/png",
              ".jpg": "image/jpeg",
              ".jpeg": "image/jpeg",
              ".gif": "image/gif",
              ".webp": "image/webp",
            }[path.extname(file)] || "text/plain; charset=utf-8",
          );
          response.end(await readFile(file));
        } catch (error) {
          response.statusCode = error.code === "ENOENT" ? 404 : 500;
          response.end(
            response.statusCode === 404 ? "Not found" : error.message,
          );
        }
      });
    },
    configurePreviewServer(server) {
      if (mode === "console") return;
      // Vite otherwise returns the SPA with status 200 for missing docs/assets.
      const output = path.resolve(
        server.config.root,
        server.config.build.outDir,
      );
      server.middlewares.use(async (request, response, next) => {
        const pathname = new URL(request.url, "http://localhost").pathname;
        if (pathname === "/builder/") {
          response.statusCode = 308;
          response.setHeader(
            "Location",
            "/builder" + new URL(request.url, "http://localhost").search,
          );
          response.end();
          return;
        }
        if (pathname === "/builder") {
          request.url = "/builder.html";
          return next();
        }
        if (
          pathname !== "/" &&
          !pathname.endsWith("/") &&
          !/\.[^/]+$/.test(pathname)
        ) {
          response.statusCode = 308;
          response.setHeader(
            "Location",
            pathname + "/" + new URL(request.url, "http://localhost").search,
          );
          response.end();
          return;
        }
        try {
          const file = path.resolve(output, `.${decodeURIComponent(pathname)}`);
          const info =
            (file === output || file.startsWith(`${output}/`)) &&
            (await stat(file).catch(() => null));
          if (
            info?.isFile() ||
            (info?.isDirectory() &&
              (
                await stat(path.join(file, "index.html")).catch(() => null)
              )?.isFile())
          )
            return next();
          response.statusCode = 404;
          response.setHeader("Content-Type", "text/html; charset=utf-8");
          const content = await readFile(
            path.join(
              output,
              pathname.startsWith("/docs/") ? "docs/404.html" : "404.html",
            ),
          );
          response.end(request.method === "HEAD" ? undefined : content);
        } catch {
          response.statusCode = 404;
          response.end("Not found");
        }
      });
    },
    generateBundle: {
      order: "post",
      async handler(_options, bundle) {
        if (mode === "console") return;
        const data = await publication();
        const template = bundle["index.html"].source;
        const originValue = siteOrigin(origin);
        const emit = (fileName, source) =>
          this.emitFile({ type: "asset", fileName, source });
        async function files(directory) {
          const result = [];
          for (const entry of await readdir(directory, {
            withFileTypes: true,
          })) {
            const file = path.join(directory, entry.name);
            result.push(...(entry.isDirectory() ? await files(file) : [file]));
          }
          return result;
        }
        const publicRoot = path.join(generated, "public");
        for (const file of await files(publicRoot))
          emit(`docs/${path.relative(publicRoot, file)}`, await readFile(file));
        for (const page of data.pages) {
          const fullPage = JSON.parse(
            await readFile(
              path.join(publicRoot, page.dataUrl.slice("/docs/".length)),
              "utf8",
            ),
          );
          emit(
            `${page.route.slice(1)}index.html`,
            pageHtml(template, fullPage, originValue),
          );
        }
        emit(
          "docs/404.html",
          pageHtml(
            template,
            {
              route: "/docs/404/",
              indexable: false,
              title: "404 — Page not found",
              description: "Documentation page not found.",
              html: '<p>Return to <a href="/docs/">documentation</a>.</p>',
            },
            originValue,
          ),
        );
      },
    },
  };
}
