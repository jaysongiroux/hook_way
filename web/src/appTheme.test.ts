/// <reference types="node" />
import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { DARK } from "./tokens";

const read = (rel: string) => readFileSync(resolve(__dirname, "..", rel), "utf8");

test("the document carries the canvas and colour scheme", () => {
  const html = read("index.html");
  expect(html).toContain(DARK.bg);
  expect(html).toContain("color-scheme: light");
  expect(html).toContain('href="/favicon.ico"');
  expect(html).not.toContain("favicon.svg");
});

test("the app forces the light colour scheme for the builder too", () => {
  expect(read("src/main.tsx")).toContain('forceColorScheme="light"');
});

test("the builder canvas runs in light colour mode", () => {
  expect(read("src/graph/Canvas.tsx")).toContain('colorMode="light"');
});
