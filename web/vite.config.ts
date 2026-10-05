/// <reference types="vitest/config" />
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);

// base "/" so the SPA's absolute asset URLs resolve when the Go editor serves
// it at the server root.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "REPLAY_API_URL");
  return ({
  base: "/",
  plugins: [
    react(),
    {
      name: "product-entry",
      configureServer(server) {
        if (mode !== "console") return;
        server.middlewares.use((req, res, next) => {
          if (req.url?.split("?")[0] === "/runtime-config.json") {
            res.setHeader("Content-Type", "application/json");
            res.setHeader("Cache-Control", "no-store");
            res.end(JSON.stringify({ replayApiUrl: env.REPLAY_API_URL || "" }));
            return;
          }
          if (req.url === "/" || req.url?.split("?")[0] === "/builder") req.url = "/console.html";
          next();
        });
      },
      configurePreviewServer(server) {
        if (mode !== "console") return;
        server.middlewares.use((req, res, next) => {
          if (req.url?.split("?")[0] === "/runtime-config.json") {
            res.setHeader("Content-Type", "application/json");
            res.setHeader("Cache-Control", "no-store");
            res.end(JSON.stringify({ replayApiUrl: env.REPLAY_API_URL || "" }));
            return;
          }
          if (req.url === "/" || req.url?.split("?")[0] === "/builder") req.url = "/console.html";
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
            /\/src\/AppRoutes\.tsx/.test(id)
          ) {
            this.error(`Product imports marketing code: ${id}`);
          }
        }
      },
    },
  ],
  server: { fs: { allow: [repoRoot] } },
  build: {
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
});
});
