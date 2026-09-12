/// <reference types="node" />
import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { DARK } from "./tokens";

const read = (rel: string) => readFileSync(resolve(__dirname, "..", rel), "utf8");

// A white band appeared on overscroll because the document itself was still
// light, and native scrollbars rendered light for the same reason.
test("the document carries the dark canvas and colour scheme", () => {
  const html = read("index.html");
  expect(html).toContain(DARK.bg);
  expect(html).toContain("color-scheme: dark");
});

// Both routes are dark, so the provider forces it rather than following the
// operating system preference.
test("the app forces the dark colour scheme for the builder too", () => {
  expect(read("src/main.tsx")).toContain('forceColorScheme="dark"');
});

// React Flow paints its own canvas, controls and selection styles, which stay
// light unless it is told the colour mode.
test("the builder canvas runs in dark colour mode", () => {
  expect(read("src/graph/Canvas.tsx")).toContain('colorMode="dark"');
});
