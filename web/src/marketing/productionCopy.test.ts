import { expect, test } from "vitest";
import { PRODUCTION_CARDS } from "./productionCopy";
import { COMPARE_ROWS } from "./compareCopy";

// Six two-column tiles fill the six-column bento exactly: two full rows.
test("the section covers six themes as compact tiles", () => {
  expect(PRODUCTION_CARDS).toHaveLength(6);
  for (const c of PRODUCTION_CARDS) {
    expect(c.title.length).toBeGreaterThan(0);
    expect(c.valueLabel.length).toBeGreaterThan(0);
    // One short line per tile; the long-form detail lives in the docs.
    expect(c.line.length).toBeLessThan(120);
    expect(c.line.length).toBeGreaterThan(20);
  }
});

// Each headline value is a count a reader could verify in the repo.
test("every tile headlines a real number", () => {
  const values = PRODUCTION_CARDS.map((c) => c.value);
  expect(values).toEqual(["256", "3", "45", "11", "1", "7"]);
});

// Retry blocks exist for rabbitmq, kafka, and webhook only.
test("retry claims name the destinations that actually support them", () => {
  const retry = PRODUCTION_CARDS.find((c) => c.id === "retries")!;
  const text = `${retry.valueLabel} ${retry.line}`;
  expect(text).toMatch(/webhook/i);
  expect(text).toMatch(/Kafka/);
  expect(text).toMatch(/RabbitMQ/);
  expect(text).not.toMatch(/every destination|all destinations/i);
});

// Retrying is not the same capability as dead-lettering or honouring a 429,
// and the tile used to run all three together over the same subject list.
// Kafka has no dead-letter path at all (nothing in internal/queue/kafka*.go),
// and only webhook and node_http_request parse Retry-After -- Kafka and
// RabbitMQ speak binary protocols and cannot return an HTTP 429. The same two
// assertions live in internal/marketingfacts so the Go side fails too.
test("the retry tile scopes dead-lettering and 429 handling to webhook", () => {
  const retry = PRODUCTION_CARDS.find((c) => c.id === "retries")!;
  const sentences = retry.line.split(/(?<=[.;])\s+/);

  const deadLetter = sentences.filter((s) => /dead[- ]letter/i.test(s));
  expect(deadLetter.length).toBeGreaterThan(0);
  for (const s of deadLetter) {
    expect(s, `dead-letter claim must not name Kafka: "${s}"`).not.toMatch(/Kafka/i);
  }

  const retryAfter = sentences.filter((s) => /429/.test(s));
  expect(retryAfter.length).toBeGreaterThan(0);
  for (const s of retryAfter) {
    expect(s, `429 claim must not name Kafka: "${s}"`).not.toMatch(/Kafka/i);
    expect(s, `429 claim must not name RabbitMQ: "${s}"`).not.toMatch(/RabbitMQ/i);
  }
});

// Dead-lettering is opt-in and async-only; the default on terminal failure is
// log-and-drop. A bare "then dead-letters" title implied a default.
test("the retry tile does not present dead-lettering as a default", () => {
  const retry = PRODUCTION_CARDS.find((c) => c.id === "retries")!;
  expect(retry.title).toMatch(/optional/i);
});

test("the copy claims nothing the project does not implement", () => {
  const text = PRODUCTION_CARDS.flatMap((c) => [c.title, c.valueLabel, c.line])
    .join(" ")
    .toLowerCase();
  for (const absent of [
    "hot reload",
    "secret rotation",
    "rbac",
    "single sign-on",
    "audit log",
    "opentelemetry",
    "helm",
    "encryption at rest",
    "lame-duck",
    "per second",
    "uptime",
    "open source",
  ]) {
    expect(text, `copy must not claim "${absent}"`).not.toContain(absent);
  }
});

// The replay service is optional and operator-driven: say so in plain words,
// and never claim a UI, automatic re-drive, or exactly-once delivery.
const overclaims = [/automatic/i, /\bUI\b/, /dashboard/i, /exactly/i, /guarantee/i];

test("the replay comparison is partial and honest", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "replay")!;
  const hookway = row.values.hookway;
  expect(hookway.level).toBe("partial");
  expect(hookway.detail).toMatch(/optional replay service/i);
  expect(hookway.detail).toMatch(/\bAPI\b/);
  for (const re of overclaims) expect(hookway.detail).not.toMatch(re);
});

// The comparison cell once read "stores acknowledged failed deliveries",
// which is how the code talks, not how a reader does.
test("replay copy speaks the reader's language, not the implementation's", () => {
  const replayCopy = [
    COMPARE_ROWS.find((r) => r.id === "replay")!.values.hookway.detail,
    ...PRODUCTION_CARDS.filter((c) => c.id === "replay").flatMap((c) => [c.title, c.valueLabel, c.line]),
  ].join(" ");
  for (const jargon of [/acknowledged/i, /redrive|re-drive/i, /capture/i, /snapshot/i, /record/i]) {
    expect(replayCopy).not.toMatch(jargon);
  }
});

test("the replay tile headlines the default retention and stays opt-in", () => {
  const replay = PRODUCTION_CARDS.find((c) => c.id === "replay")!;
  expect(replay.value).toBe("7");
  expect(replay.valueLabel).toMatch(/days/);
  expect(replay.line).toMatch(/optional/i);
  expect(replay.line).toMatch(/retry/i);
  for (const re of overclaims) expect(`${replay.title} ${replay.line}`).not.toMatch(re);
});

// "Ships as one binary" is true of core; the tile has to say replay is an
// extra you choose to run.
test("the packaging tile says replay is opt-in", () => {
  const packaging = PRODUCTION_CARDS.find((c) => c.id === "packaging")!;
  expect(packaging.line).toMatch(/unless you add replay/i);
});

// Core serves plain HTTP; its TLS 1.2 minimum is on the connections it makes
// to destinations. An unscoped "TLS 1.2 floor" reads as inbound TLS.
test("TLS claims say they are about outbound connections", () => {
  for (const c of PRODUCTION_CARDS) {
    for (const text of [c.title, c.valueLabel, c.line]) {
      if (/TLS/.test(text)) expect(text, `"${text}"`).toMatch(/outbound/i);
    }
  }
});

// Capture fires on any 2xx, and the dev flow answers 202: "its 200" is wrong.
test("replay copy says 2xx, not 200", () => {
  const replayCopy = [
    COMPARE_ROWS.find((r) => r.id === "replay")!.values.hookway.detail,
    ...PRODUCTION_CARDS.filter((c) => c.id === "replay").flatMap((c) => [c.title, c.valueLabel, c.line]),
  ].join(" ");
  expect(replayCopy).toMatch(/2xx/);
  expect(replayCopy).not.toMatch(/\b200\b/);
});
