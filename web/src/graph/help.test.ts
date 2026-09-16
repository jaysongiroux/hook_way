import { expect, test } from "vitest";
import { NODE_TYPES } from "./nodeSchema";
import { missingNodeHelp, nodeHelp, nodeFieldHelp, SERVER_FIELD_HELP } from "./help";

test("every catalog node type has a one-sentence description", () => {
  expect(missingNodeHelp()).toEqual([]);
  for (const type of NODE_TYPES) {
    expect(nodeHelp(type).length).toBeGreaterThan(20);
  }
});

test("envelope field help explains decoding", () => {
  expect(nodeFieldHelp("envelope", "body_format")).toMatch(/decode/i);
});

test("server settings include copy for addr", () => {
  expect(SERVER_FIELD_HELP.addr).toMatch(/listen/i);
});
