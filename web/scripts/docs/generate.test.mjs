import test from "node:test";
import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  writeFile,
  readFile,
  rm,
  symlink,
} from "node:fs/promises";
import { execFileSync } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { createCatalog, repoRoot } from "./catalog.mjs";
import { generate } from "./generate.mjs";

async function fixture(t) {
  const root = await mkdtemp(path.join(tmpdir(), "hookway-publication-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  async function put(source, text) {
    await mkdir(path.dirname(path.join(root, source)), { recursive: true });
    await writeFile(path.join(root, source), text);
  }
  await put(
    "web/docs-publication.yaml",
    await readFile(path.join(repoRoot, "web/docs-publication.yaml")),
  );
  await put(
    ".gitignore",
    "examples/private/\ndocs/local-plans/\nweb/.generated/\n",
  );
  await put("web/public/hookway-icon-512.png", "brand");
  await put("web/public/favicon.ico", "icon");
  await put("README.md", "# Hookway\n\n[Docs](docs/README.md)\n");
  await put(
    "docs/README.md",
    "# Documentation\n\n[Guide](guides/test.md#repeat-1)\n\n[Docker](dockerhub/README.md)\n\n[API](api/editor.json)\n",
  );
  await put(
    "docs/guides/test.md",
    '# Guide\n\n## Repeat\n\nText.\n\n## Repeat\n\n[Example][config]\n\n[config]: ../../examples/config.yaml\n\n```sh\necho "[unchanged](missing.md)"\n```\n',
  );
  await put("docs/dockerhub/README.md", "# Docker images\n");
  await put("docs/api/editor.json", "{}\n");
  await put(
    "examples/config.yaml",
    '# Config example\nvalue: "${KEEP_THIS}"\n',
  );
  await put(
    "examples/showcase/invalid-many.yaml",
    "# Expected to fail validation\nunknown: true\n",
  );
  // Tracking synthetic files happens only in this isolated temporary repository.
  execFileSync("git", ["init", "-q"], { cwd: root });
  execFileSync("git", ["add", "."], { cwd: root });
  await put("docs/local-plans/private.md", "# Never publish\n");
  await put("docs/untracked.md", "# Not added to Git\n");
  await put("examples/private/secret.yaml", "secret: private\n");
  return { root, put };
}

test("discovery respects Git tracking and exclusions; new non-ignored examples are automatic", async (t) => {
  const { root, put } = await fixture(t);
  await put("examples/new-folder/new.yml", "value: new\n");
  const catalog = await createCatalog(root);
  assert.equal(catalog.links["docs/untracked.md"], undefined);
  assert.equal(catalog.links["docs/local-plans/private.md"], undefined);
  assert.equal(catalog.links["docs/dockerhub/README.md"], undefined);
  assert.equal(catalog.links["docs/api/editor.json"], undefined);
  assert.equal(catalog.links["examples/private/secret.yaml"], undefined);
  assert.equal(
    catalog.links["examples/new-folder/new.yml"],
    "/docs/examples/new-folder/new/",
  );
  assert.equal(
    catalog.links["examples/new-folder"],
    "/docs/examples/new-folder/",
  );
  assert(catalog.links["examples/showcase/invalid-many.yaml"]);
  execFileSync("git", ["add", "docs/untracked.md"], { cwd: root });
  assert.equal(
    (await createCatalog(root)).links["docs/untracked.md"],
    "/docs/untracked/",
  );
  await generate(root);
  const landing = await readFile(
    path.join(root, "web/.generated/docs/markdown/index.md"),
    "utf8",
  );
  assert.match(landing, /\[Not added to Git\]\(\/docs\/untracked\/\)/);
});

test("generation rewrites reference links and preserves code, original sources and download bytes", async (t) => {
  const { root } = await fixture(t);
  const source = await readFile(path.join(root, "docs/guides/test.md"));
  const yaml = await readFile(path.join(root, "examples/config.yaml"));
  await generate(root);
  const guide = await readFile(
    path.join(root, "web/.generated/docs/markdown/guides/test.md"),
    "utf8",
  );
  assert.match(guide, /\[config\]: \/docs\/examples\/config\//);
  assert.match(guide, /echo "\[unchanged\]\(missing\.md\)"/);
  const index = await readFile(
    path.join(root, "web/.generated/docs/markdown/index.md"),
    "utf8",
  );
  assert.match(
    index,
    /github\.com\/jaysongiroux\/hook_way\/blob\/main\/docs\/api\/editor.json/,
  );
  assert.match(
    index,
    /github\.com\/jaysongiroux\/hook_way\/blob\/main\/docs\/dockerhub\/README.md/,
  );
  assert.deepEqual(
    await readFile(
      path.join(root, "web/.generated/docs/public/raw/examples/config.yaml"),
    ),
    yaml,
  );
  assert.deepEqual(
    await readFile(path.join(root, "docs/guides/test.md")),
    source,
  );
  await assert.rejects(
    readFile(
      path.join(root, "web/.generated/docs/public/raw/docs/api/editor.json"),
    ),
  );
});

test("new exclusions and deleted files disappear from generated pages and downloads", async (t) => {
  const { root } = await fixture(t);
  await generate(root);
  await rm(path.join(root, "examples/config.yaml"));
  await writeFile(
    path.join(root, "docs/guides/test.md"),
    "# Guide\n\n## Repeat\n\n## Repeat\n",
  );
  const config = path.join(root, "web/docs-publication.yaml");
  await writeFile(
    config,
    (await readFile(config, "utf8")).replace(
      '  - "docs/dockerhub/**"',
      '  - "docs/dockerhub/**"\n  - "examples/showcase/**"',
    ),
  );
  await generate(root);
  await assert.rejects(
    readFile(
      path.join(root, "web/.generated/docs/markdown/examples/config.md"),
    ),
  );
  await assert.rejects(
    readFile(
      path.join(root, "web/.generated/docs/public/raw/examples/config.yaml"),
    ),
  );
  await assert.rejects(
    readFile(
      path.join(
        root,
        "web/.generated/docs/markdown/examples/showcase/invalid-many.md",
      ),
    ),
  );
});

test("private docs links, missing headings and route collisions fail with source context", async (t) => {
  const { root, put } = await fixture(t);
  await put("docs/guides/test.md", "# Guide\n\n[Private](../untracked.md)\n");
  await assert.rejects(generate(root), /untracked documentation target/);
  await put("docs/guides/test.md", "# Guide\n");
  await assert.rejects(generate(root), /heading missing/);
  await put("examples/config.md", "# Collision\n");
  await assert.rejects(createCatalog(root), /Duplicate documentation route/);
});

test("symlink examples are rejected and dry-run leaves generated output absent", async (t) => {
  const { root } = await fixture(t);
  await generate(root, { dryRun: true });
  await assert.rejects(
    readFile(path.join(root, "web/.generated/docs/manifest.json")),
  );
  await symlink(
    path.join(root, "README.md"),
    path.join(root, "examples/link.yaml"),
  );
  await assert.rejects(createCatalog(root), /Symlink cannot be published/);
});

test("compiled HTML preserves duplicate heading anchors and strips active source HTML", async (t) => {
  const { root, put } = await fixture(t);
  await put(
    "docs/guides/test.md",
    '# Guide\n\n## Repeat\n\n## Repeat\n\n<script>alert("unsafe")</script>\n\n<a href="javascript:alert(1)">Bad link</a>\n',
  );
  await generate(root);
  const page = JSON.parse(
    await readFile(
      path.join(root, "web/.generated/docs/public/data/guides/test.json"),
      "utf8",
    ),
  );
  assert.match(page.html, /id="repeat-1"/);
  assert.doesNotMatch(page.html, /<script|javascript:/);
  assert.equal(page.headings[1].id, "repeat-1");
  assert.equal(
    page.sourceUrl,
    "https://github.com/jaysongiroux/hook_way/blob/main/docs/guides/test.md",
  );
  assert.equal(page.editUrl, undefined);
  assert.doesNotMatch(page.description, /<[^>]+>/);
  assert.doesNotMatch(page.searchText, /data-source-title|<script/);
});
