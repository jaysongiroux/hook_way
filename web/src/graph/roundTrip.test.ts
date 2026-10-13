import { expect, test } from "vitest";
import { parse } from "yaml";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";
import { layout } from "./layout";

const examplesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  "../../../examples/showcase",
);

function normalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(value as object).sort()) {
      out[k] = normalize((value as Record<string, unknown>)[k]);
    }
    return out;
  }
  return value;
}

test.each(readdirSync(examplesDir).filter((f) => f.endsWith(".yaml")))(
  "round-trips %s without field loss",
  (file) => {
    const yaml = readFileSync(join(examplesDir, file), "utf8");
    const parsed = parse(yaml) as Record<string, unknown>;
    const model = fromConfig(parsed);
    const { config } = toConfig({
      ...model,
      nodes: layout(model.nodes, model.edges),
    });
    expect(normalize(config)).toEqual(normalize(parsed));
  },
);

// examples/xml.yaml and examples/multipart.yaml both sit outside the showcase
// sweep above, so neither body format was covered by an import/export
// round-trip until these two cases.
test("round-trips the xml example, prune rules included", () => {
  const path = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../examples/xml.yaml",
  );
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const model = fromConfig(parsed);
  const { config } = toConfig({
    ...model,
    nodes: layout(model.nodes, model.edges),
  });
  // xml_prune's `rules` is the only array-of-strings option on a transform;
  // naming it keeps the test from passing on two configs that both dropped it.
  expect(JSON.stringify(config)).toContain("xml_prune");
  expect(normalize(config)).toEqual(normalize(parsed));
});

// examples/multipart.yaml is outside the showcase sweep above, but its nested
// `multipart:` option block is the first nested object on a non-destination
// node — if the builder's import/export path flattened or dropped it, the
// block an operator typed in the inspector would vanish on export.
test("round-trips the multipart example, nested option block included", () => {
  const path = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../examples/multipart.yaml",
  );
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const model = fromConfig(parsed);
  const { config } = toConfig({
    ...model,
    nodes: layout(model.nodes, model.edges),
  });
  // Named explicitly so the test fails loudly if the block is ever dropped,
  // rather than passing by comparing two configs that both lost it.
  expect(JSON.stringify(config)).toContain("max_part_bytes");
  expect(normalize(config)).toEqual(normalize(parsed));
});

// examples/connections.yaml exercises the connections block, which the
// builder manages rather than passing through configExtra. It also carries a
// replay block whose connection: sibling is not something the builder edits,
// so this is the case that catches a managed key being emitted in the wrong
// place or dropped entirely.
test("round-trips the connections example, replay reference included", () => {
  const path = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../examples/connections.yaml",
  );
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const model = fromConfig(parsed);
  expect(Object.keys(model.connections)).toEqual(["aws_prod", "pg_main"]);
  const { config } = toConfig({
    ...model,
    nodes: layout(model.nodes, model.edges),
  });
  expect(normalize(config)).toEqual(normalize(parsed));
});

test("flow-level fields the builder does not model survive a round trip", () => {
  const src = {
    server: { addr: ":8080", max_body_bytes: 1048576 },
    replay: {
      url: "https://replay.internal",
      capture_token: "${REPLAY_CAPTURE_TOKEN}",
    },
    flows: {
      first: {
        capture_failures: false,
        nodes: [{ id: "in", type: "http_trigger", path: "/a" }],
        edges: [],
      },
      second: {
        capture_failures: true,
        delivery_failure_policy: "skip_failed",
        nodes: [{ id: "in", type: "http_trigger", path: "/b" }],
        edges: [],
      },
    },
  };
  const { config } = toConfig(fromConfig(src));
  const flows = config.flows as Record<string, Record<string, unknown>>;
  expect(flows.first.capture_failures).toBe(false);
  expect(flows.second.capture_failures).toBe(true);
  expect(flows.second.delivery_failure_policy).toBe("skip_failed");
  expect(config.replay).toEqual(src.replay);
});

test("preserves an invalid core replay block until it is replaced", () => {
  const src = {
    server: { addr: ":8080" },
    replay: "invalid",
    flows: { main: { nodes: [], edges: [] } },
  };
  const doc = fromConfig(src);
  expect(doc.coreReplayConfig).toBeNull();
  expect(toConfig(doc).config.replay).toBe("invalid");
});

test("round-trips broker body, FIFO identities, retry and response policies", () => {
  const path = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../examples/destinations/fifo-sqs.yaml",
  );
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const { config } = toConfig(fromConfig(parsed));
  expect(JSON.stringify(config)).toContain("json_js_sha256");
  expect(normalize(config)).toEqual(normalize(parsed));
});

test.each(["auth/request-signatures.yaml", "destinations/sqs.yaml"])(
  "round-trips auth inputs and standard broker options in %s",
  (example) => {
    const path = join(
      dirname(fileURLToPath(import.meta.url)),
      `../../../examples/${example}`,
    );
    const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
    const { config } = toConfig(fromConfig(parsed));
    expect(normalize(config)).toEqual(normalize(parsed));
  },
);

test.each(["routing/shared.yaml", "routing/admission.yaml"])(
  "round-trips routing bindings and admission in %s",
  (example) => {
    const path = join(
      dirname(fileURLToPath(import.meta.url)),
      `../../../examples/${example}`,
    );
    const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
    const { config } = toConfig(fromConfig(parsed));
    expect(normalize(config)).toEqual(normalize(parsed));
  },
);

for (const file of ["request-contract.yaml", "admitted.yaml"]) {
  test(`round-trips proxy contract ${file}`, () => {
    const path = join(
      dirname(fileURLToPath(import.meta.url)),
      "../../../examples/proxy",
      file,
    );
    const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
    const model = fromConfig(parsed);
    const { config } = toConfig({
      ...model,
      nodes: layout(model.nodes, model.edges),
    });
    expect(normalize(config)).toEqual(normalize(parsed));
  });
}

test.each([
  "credentials/static.yaml",
  "credentials/oauth.yaml",
  "credentials/token-exchange.yaml",
  "routing/registration.yaml",
])("round-trips native credentials and registration in %s", (file) => {
  const path = join(
    dirname(fileURLToPath(import.meta.url)),
    "../../../examples",
    file,
  );
  const parsed = parse(readFileSync(path, "utf8")) as Record<string, unknown>;
  const { config } = toConfig(fromConfig(parsed));
  expect(normalize(config)).toEqual(normalize(parsed));
  expect(JSON.stringify(config)).not.toContain("synthetic-password");
});

for (const file of readdirSync(
  join(dirname(fileURLToPath(import.meta.url)), "../../../examples/adapters"),
)) {
  test(`round-trips bounded adapters in ${file}`, () => {
    const raw = readFileSync(
      join(
        dirname(fileURLToPath(import.meta.url)),
        "../../../examples/adapters",
        file,
      ),
      "utf8",
    );
    const parsed = parse(raw) as Record<string, unknown>;
    const { config } = toConfig(fromConfig(parsed));
    expect(normalize(config)).toEqual(normalize(parsed));
  });
}
