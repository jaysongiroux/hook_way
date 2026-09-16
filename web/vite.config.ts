/// <reference types="vitest/config" />
import path from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// base "/" so the SPA's absolute asset URLs resolve when the Go editor serves
// it at the server root.
export default defineConfig({
  base: "/",
  plugins: [react()],
  server: { fs: { allow: [repoRoot] } },
  build: { outDir: "dist", emptyOutDir: true },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: "./src/setupTests.ts",
    css: true,
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
});
