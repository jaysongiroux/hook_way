import { expect, test } from "vitest";
import { PRODUCTION_CARDS } from "./productionCopy";

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
  expect(values).toEqual(["256", "3", "45", "11", "4", "1"]);
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
