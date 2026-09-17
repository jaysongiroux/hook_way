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

test("http_request retry copy resolves for the block and its nested keys", () => {
  expect(nodeFieldHelp("http_request", "retry")).toMatch(/single attempt/i);
  // SchemaForm's nestedHelp asks for the qualified name first; these must
  // resolve or the retry sub-form renders with no description at all.
  expect(nodeFieldHelp("http_request", "retry.max_attempts")).toMatch(/no retry/i);
  expect(nodeFieldHelp("http_request", "retry.initial_backoff")).toMatch(/jitter/i);
  expect(nodeFieldHelp("http_request", "retry.max_backoff")).toMatch(/Retry-After/i);
});
