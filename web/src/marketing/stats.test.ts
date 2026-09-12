import { expect, test } from "vitest";
import { DEST_TYPES } from "../graph/destTypes";
import { STATS } from "./stats";

test("the row carries four countable facts", () => {
  expect(STATS).toHaveLength(4);
});

// The destination count is read from the builder's registry rather than typed
// in, so adding a destination type updates the headline automatically.
test("the destination count is derived from the builder's registry", () => {
  const dest = STATS.find((s) => s.label.toLowerCase().includes("destination"))!;
  expect(dest.value).toBe(DEST_TYPES.length);
});

// Stat-tile contract: label in sentence case with no trailing colon.
test("labels follow the stat-tile contract", () => {
  for (const s of STATS) {
    expect(s.label).not.toMatch(/:$/);
    expect(s.label[0]).toBe(s.label[0].toUpperCase());
    expect(s.label.slice(1)).toBe(s.label.slice(1).toLowerCase());
    expect(s.value).toBeGreaterThan(0);
    expect(Number.isInteger(s.value)).toBe(true);
  }
});

// Each number is a count of something a reader could go and verify. None of
// them are performance claims, which this project has never measured.
test("every stat names what was counted", () => {
  for (const s of STATS) {
    expect(s.detail.length).toBeGreaterThan(0);
  }
  const text = STATS.map((s) => `${s.label} ${s.detail}`).join(" ").toLowerCase();
  for (const perf of ["per second", "latency", "uptime", "throughput", "ms "]) {
    expect(text, `stats must not imply a performance claim ("${perf}")`).not.toContain(perf);
  }
});
