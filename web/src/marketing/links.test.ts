/// <reference types="node" />
import { expect, test } from "vitest";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { DOC_LINKS, EXAMPLES, GITHUB_REF, GITHUB_REPO, repoBlob, repoTree } from "./links";

const repoRoot = resolve(__dirname, "../../..");

test("links point at the configured GitHub repo", () => {
  expect(GITHUB_REPO).toBe("https://github.com/jaysongiroux/hook_way");
  expect(GITHUB_REF).toBe("v2");
  expect(repoBlob("README.md")).toBe("https://github.com/jaysongiroux/hook_way/blob/v2/README.md");
  expect(repoTree("examples")).toBe("https://github.com/jaysongiroux/hook_way/tree/v2/examples");
});

// The marketing site deliberately links out instead of restating the docs, so a
// renamed or deleted file would silently become a 404 for every visitor.
test("every linked documentation file exists in the repo", () => {
  for (const link of DOC_LINKS) {
    expect(existsSync(resolve(repoRoot, link.path)), `${link.path} is linked but missing`).toBe(true);
  }
});

test("the linked examples directory exists in the repo", () => {
  expect(existsSync(resolve(repoRoot, EXAMPLES.path))).toBe(true);
});

test("each documentation link carries a title and a description", () => {
  expect(DOC_LINKS.length).toBeGreaterThan(3);
  for (const link of DOC_LINKS) {
    expect(link.title.length).toBeGreaterThan(0);
    expect(link.description.length).toBeGreaterThan(0);
    expect(link.href).toContain(GITHUB_REPO);
  }
});
