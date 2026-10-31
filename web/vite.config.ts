/// <reference types="vitest/config" />
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv, type Plugin, type UserConfig } from "vite";
import react from "@vitejs/plugin-react";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

// base "/" so the SPA's absolute asset URLs resolve when the Go editor serves
// it at the server root.
export default defineConfig(async ({ mode }) => {
  const { documentationPlugin } = await import(
    path.join(repoRoot, "web/scripts/docs/plugin.mjs")
  );
  const docsPlugin: Plugin = documentationPlugin(
    mode,
    process.env.SITE_URL || loadEnv(mode, process.cwd(), "SITE_URL").SITE_URL,
  );
  const env = loadEnv(mode, process.cwd(), "REPLAY_API_URL");
  return {
    base: "/",
    plugins: [
      react(),
      docsPlugin,
      {
        name: "product-entry",
        configureServer(server) {
          if (mode !== "console") return;
          server.middlewares.use((req, res, next) => {
            if (req.url?.split("?")[0] === "/runtime-config.json") {
              res.setHeader("Content-Type", "application/json");
              res.setHeader("Cache-Control", "no-store");
              res.end(
                JSON.stringify({ replayApiUrl: env.REPLAY_API_URL || "" }),
              );
              return;
            }
            if (req.url === "/" || req.url?.split("?")[0] === "/builder")
              req.url = "/console.html";
            next();
          });
        },
        configurePreviewServer(server) {
          if (mode !== "console") return;
          server.middlewares.use((req, res, next) => {
            if (req.url?.split("?")[0] === "/runtime-config.json") {
              res.setHeader("Content-Type", "application/json");
              res.setHeader("Cache-Control", "no-store");
              res.end(
                JSON.stringify({ replayApiUrl: env.REPLAY_API_URL || "" }),
              );
              return;
            }
            if (req.url === "/" || req.url?.split("?")[0] === "/builder")
              req.url = "/console.html";
            next();
          });
        },
      },
      {
        name: "console-import-boundary",
        generateBundle() {
          if (mode !== "console") return;
          for (const id of Array.from(this.getModuleIds())) {
            if (
              /\/src\/marketing\//.test(id) ||
              /\/src\/AppRoutes\.tsx/.test(id) ||
              /\/src\/website\//.test(id) ||
              /\/node_modules\/(?:posthog-js|@posthog)\//.test(id)
            ) {
              this.error(`Product imports marketing code: ${id}`);
            }
          }
        },
      },
    ],
    server: { fs: { allow: [repoRoot] } },
    ...(mode !== "console"
      ? {
          preview: {
            port: Number(process.env.WEBSITE_PORT || 4173),
            host: "127.0.0.1",
            strictPort: true,
          },
        }
      : {}),
    build: {
      manifest: mode !== "console",
      outDir: mode === "console" ? "dist-console" : "dist",
      emptyOutDir: true,
      rollupOptions: {
        input: mode === "console" ? "console.html" : "index.html",
      },
    },
    test: {
      globals: true,
      environment: "jsdom",
      setupFiles: "./src/setupTests.ts",
      css: true,
      include: ["src/**/*.{test,spec}.{ts,tsx}"],
    },
  } satisfies UserConfig;
});
