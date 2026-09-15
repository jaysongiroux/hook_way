import { expect, test } from "vitest";
import { PRODUCTION_CARDS } from "./productionCopy";

test("the section covers six themes as compact tiles", () => {
  expect(PRODUCTION_CARDS).toHaveLength(6);
  expect(new Set(PRODUCTION_CARDS.map((c) => c.accent)).size).toBe(6);
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
  expect(values).toEqual(["256", "3", "42", "11", "4", "1"]);
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
