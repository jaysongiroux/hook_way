import { expect, test } from "vitest";
import { PRODUCTION_CARDS } from "./productionCopy";
import { COMPARE_ROWS } from "./compareCopy";

// Keep operational details available behind the benchmark summary.
test("operational details cover the six configuration themes", () => {
  expect(PRODUCTION_CARDS).toHaveLength(6);
  for (const c of PRODUCTION_CARDS) {
    expect(c.title.length).toBeGreaterThan(0);
    expect(c.valueLabel.length).toBeGreaterThan(0);
    // One short line per tile; the long-form detail lives in the docs.
    expect(c.line.length).toBeLessThan(180);
    expect(c.line.length).toBeGreaterThan(20);
  }
});

// Each operational default is a value a reader can verify in the repo.
test("operational defaults retain their source-checked values", () => {
  const values = PRODUCTION_CARDS.map((c) => c.value);
  expect(values).toEqual(["256", "3", "76", "15", "1", "7"]);
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
    expect(s, `dead-letter claim must not name Kafka: "${s}"`).not.toMatch(
      /Kafka/i,
    );
  }

  const retryAfter = sentences.filter((s) => /429/.test(s));
  expect(retryAfter.length).toBeGreaterThan(0);
  for (const s of retryAfter) {
    expect(s, `429 claim must not name Kafka: "${s}"`).not.toMatch(/Kafka/i);
    expect(s, `429 claim must not name RabbitMQ: "${s}"`).not.toMatch(
      /RabbitMQ/i,
    );
  }
});

// Dead-lettering is opt-in and async-only; the default on terminal failure is
// log-and-drop. A bare "then dead-letters" title implied a default.
test("the retry tile does not present dead-lettering as a default", () => {
  const retry = PRODUCTION_CARDS.find((c) => c.id === "retries")!;
  expect(`${retry.title} ${retry.line}`).toMatch(/optional/i);
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

// The replay service is optional and operator-driven. Say that it is a full
// service, and state the operator console and absence of automatic re-drive
// rather than marking the cell partial. Never claim exactly-once delivery.
const overclaims = [/automatic/i, /exactly/i, /guarantee/i];

test("the replay comparison describes the full optional service", () => {
  const row = COMPARE_ROWS.find((r) => r.id === "replay")!;
  const hookway = row.values.hookway;
  expect(hookway.level).toBe("yes");
  expect(hookway.level).not.toBe("partial");
  expect(row.capability).toMatch(/Inspect and retry failed deliveries/);
  expect(hookway.detail).toMatch(/optional hookway-replay/i);
  expect(hookway.detail).toMatch(/inspect/i);
  expect(hookway.detail).toMatch(/download/i);
  expect(hookway.detail).toMatch(/retry/i);
  expect(hookway.detail).toMatch(/dismiss/i);
  expect(COMPARE_ROWS.find((r) => r.id === "payload-retention")!.values.hookway.detail).toMatch(/max_record_bytes defaults to 16 MiB/);
  expect(hookway.detail).toMatch(/resumes at the failed node/i);
  expect(hookway.detail).toMatch(/\bAPI\b/);
  expect(hookway.detail).toMatch(/scoped operations console/i);
  expect(hookway.detail).toMatch(/no automatic re-drive/i);
  const positive = hookway.detail.replace(/no automatic re-drive/gi, "");
  for (const re of overclaims) expect(positive).not.toMatch(re);
});

// The comparison cell once read "stores acknowledged failed deliveries",
// which is how the code talks, not how a reader does.
test("replay copy speaks the reader's language, not the implementation's", () => {
  const replayCopy = [
    COMPARE_ROWS.find((r) => r.id === "replay")!.values.hookway.detail,
    ...PRODUCTION_CARDS.filter((c) => c.id === "replay").flatMap((c) => [
      c.title,
      c.valueLabel,
      c.line,
    ]),
  ].join(" ");
  for (const jargon of [/acknowledged/i, /snapshot/i, /\brecord\b/i]) {
    expect(replayCopy).not.toMatch(jargon);
  }
  // The brief names the capture: a destination miss after the sender's 2xx.
  expect(replayCopy.replace(/captures destination misses/gi, "")).not.toMatch(
    /\bcapture/i,
  );
  // "re-drive" is allowed only when the sentence says the service does not do it.
  expect(replayCopy.replace(/no automatic re-drive/gi, "")).not.toMatch(
    /redrive|re-drive/i,
  );
});

test("the replay tile headlines the default retention and stays opt-in", () => {
  const replay = PRODUCTION_CARDS.find((c) => c.id === "replay")!;
  expect(replay.value).toBe("7");
  expect(replay.valueLabel).toMatch(/days/);
  expect(replay.line).toMatch(/optional/i);
  expect(replay.line).toMatch(/retry/i);
  for (const re of overclaims)
    expect(`${replay.title} ${replay.line}`).not.toMatch(re);
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
    ...PRODUCTION_CARDS.filter((c) => c.id === "replay").flatMap((c) => [
      c.title,
      c.valueLabel,
      c.line,
    ]),
  ].join(" ");
  expect(replayCopy).toMatch(/2xx/);
  expect(replayCopy).not.toMatch(/\b200\b/);
});
