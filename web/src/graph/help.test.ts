import { expect, test } from "vitest";
import { NODE_TYPES } from "./nodeSchema";
import {
  missingNodeHelp,
  nodeHelp,
  nodeFieldHelp,
  SERVER_FIELD_HELP,
} from "./help";

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
  expect(nodeFieldHelp("http_request", "retry.max_attempts")).toMatch(
    /no retry/i,
  );
  expect(nodeFieldHelp("http_request", "retry.initial_backoff")).toMatch(
    /jitter/i,
  );
  expect(nodeFieldHelp("http_request", "retry.max_backoff")).toMatch(
    /Retry-After/i,
  );
});

test("filter path copy mentions both body shapes", () => {
  const copy = nodeFieldHelp("filter", "conditions.path");
  expect(copy).toBeDefined();
  expect(copy).toMatch(/xml/i);
  expect(copy).toMatch(/@/);
});

test("envelope body_format copy explains that raw leaves the body empty", () => {
  expect(nodeFieldHelp("envelope", "body_format")).toMatch(
    /raw leaves the body empty/i,
  );
});

test("multipart copy resolves for the block and its nested keys", () => {
  expect(nodeFieldHelp("envelope", "multipart")).toMatch(/memory/i);
  // SchemaForm's nestedHelp asks for the qualified name first; these must
  // resolve or the multipart sub-form renders with no descriptions.
  expect(nodeFieldHelp("envelope", "multipart.max_parts")).toMatch(/413/);
  expect(nodeFieldHelp("envelope", "multipart.max_part_bytes")).toMatch(
    /1 MiB/,
  );
  expect(nodeFieldHelp("envelope", "multipart.include_content")).toMatch(
    /base64/i,
  );
});

test("body_format copy lists multipart", () => {
  expect(nodeFieldHelp("envelope", "body_format")).toMatch(/multipart/i);
});

test("code input limit copy explains what is measured", () => {
  expect(nodeFieldHelp("code", "max_input_bytes")).toMatch(/serialized input/i);
});

test("node and nested field help takes precedence over generic field copy", () => {
  expect(nodeFieldHelp("http_request", "upstream.port")).toMatch(
    /admitted upstream host/i,
  );
  expect(nodeFieldHelp("http_request", "upstream.path")).toMatch(
    /absolute path/i,
  );
  expect(nodeFieldHelp("publish", "retry.initial_backoff")).toMatch(/100ms/i);
  expect(nodeFieldHelp("http_request", "retry.initial_backoff")).toMatch(
    /500ms/i,
  );
  expect(nodeFieldHelp("publish", "fifo.group_id.value")).toMatch(
    /source is static/i,
  );
  expect(nodeFieldHelp("publish", "fifo.group_id.path")).toMatch(
    /header name/i,
  );
  expect(nodeFieldHelp("auth", "source.kind")).toMatch(
    /header or query parameter/i,
  );
  expect(nodeFieldHelp("filter", "conditions.source")).toMatch(
    /headers or the decoded event body/i,
  );
  expect(nodeFieldHelp("rate_limit", "key_by")).toMatch(
    /source_ip.*header\.Name/i,
  );
});
