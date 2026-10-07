import { NodeOptionsForm } from "./NodeOptionsForm";
import { useState } from "react";
import { expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import {
  SIMPLE_NODE_TYPES,
  CODE_EXAMPLE,
} from "./SimpleOptionsForm";
import { NODE_TYPES } from "./nodeSchema";

function setup(nodeType: string, initial: Record<string, unknown>) {
  const change = vi.fn();
  function Controlled() {
    const [value, setValue] = useState(initial);
    return (
      <MantineProvider>
        <NodeOptionsForm
          nodeType={nodeType}
          value={value}
          scope={`simple-test/${nodeType}`}
          nodeId="sample"
          onChange={(next) => {
            setValue(next);
            change(next);
          }}
        />
      </MantineProvider>
    );
  }
  render(<Controlled />);
  return change;
}

test("simple forms cover precisely the remaining runtime catalog types", () => {
  const dedicated = [
    "auth",
    "rate_limit",
    "replay_guard",
    "no_auth",
    "transform",
    "json_validate",
    "envelope",
    "filter",
    "sample",
    "inspect",
    "if_else",
    "switch",
    "publish",
    "respond",
    "http_request",
    "route",
    "register_route",
    "batch",
  ];
  expect(NODE_TYPES.filter((type) => !dedicated.includes(type)).sort()).toEqual(
    [...SIMPLE_NODE_TYPES].sort(),
  );
});

test("rate limit capacity can be edited without resetting the quota or identity source", () => {
  const change = setup("rate_limit", {
    rpm: 60,
    burst: 10,
    key_by: "header.X-Tenant-ID",
  });
  fireEvent.change(screen.getByLabelText("Maximum tracked identities"), {
    target: { value: "2000" },
  });
  expect(change).toHaveBeenLastCalledWith({
    rpm: 60,
    burst: 10,
    key_by: "header.X-Tenant-ID",
    max_keys: 2000,
  });
});

test.each([
  ["http_trigger", "Request path"],
  ["read_body", "Body read limit (bytes)"],
  ["ip_allowlist", "Allowed IPs and networks"],
  ["loop_guard", "Maximum forwarded hops"],
  ["replay_guard", "Duplicate window"],
  ["lookup", "Lookup provider"],
  ["code", "JavaScript function body"],
  ["rate_limit", "Requests per minute"],
])("%s leads with useful primary setup", (type, label) => {
  const change = setup(type, {});
  expect(screen.getByLabelText(label)).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
});

test("trigger exposes methods and preserves imported custom methods when adding another", () => {
  const change = setup("http_trigger", {
    path: "/events",
    methods: ["PROPFIND"],
    future: "retained",
  });
  expect(screen.getAllByText("PROPFIND").length).toBeGreaterThan(0);
  fireEvent.change(screen.getByLabelText("Custom HTTP method"), {
    target: { value: "subscribe" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add method" }));
  expect(change).toHaveBeenLastCalledWith({
    path: "/events",
    methods: ["PROPFIND", "SUBSCRIBE"],
    future: "retained",
  });
});

test("empty trigger methods show effective POST default without writing it", () => {
  const change = setup("http_trigger", { path: "/events" });
  expect(screen.getByPlaceholderText("Default: POST")).toBeInTheDocument();
  expect(screen.getByText(/Empty uses POST/)).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
});

test("code example uses the actual input/return contract and preserves other options", () => {
  const change = setup("code", { timeout: "2s", custom: "retained" });
  fireEvent.click(screen.getByRole("button", { name: "Use example script" }));
  expect(change).toHaveBeenLastCalledWith({
    timeout: "2s",
    custom: "retained",
    script: CODE_EXAMPLE,
  });
  expect(screen.getByLabelText("JavaScript function body")).toHaveValue(
    CODE_EXAMPLE,
  );
  fireEvent.click(screen.getByRole("button", { name: "Advanced settings" }));
  expect(screen.getByLabelText("Timeout")).toHaveValue("2s");
  expect(screen.getByLabelText("max input bytes")).toHaveAttribute(
    "placeholder",
    "Default: 1048576",
  );
});

test("read-body communicates units and required missing list feedback stays visible", () => {
  setup("read_body", { max_bytes: 1048576 });
  expect(screen.getByText(/Body read limit: 1 MiB/)).toBeInTheDocument();
  expect(screen.getByLabelText("Body read limit (bytes)")).toBeRequired();
});

test("IP allowlist starts incomplete and explains CIDR input", () => {
  setup("ip_allowlist", { cidrs: [] });
  expect(screen.getByText(/Add at least 1 item/)).toBeInTheDocument();
  expect(screen.getByText(/192\.0\.2\.10/)).toBeInTheDocument();
});

test("replay invalid duration receives actionable inline feedback and keeps its draft", () => {
  const change = setup("replay_guard", {
    ttl: "5m",
    key: "{header.X-Event-Id}",
  });
  fireEvent.change(screen.getByLabelText("Duplicate window"), {
    target: { value: "five minutes" },
  });
  expect(screen.getByText(/Use a Go duration/)).toBeInTheDocument();
  expect(screen.getByLabelText("Duplicate window")).toHaveValue("five minutes");
  expect(change).toHaveBeenLastCalledWith({
    ttl: "five minutes",
    key: "{header.X-Event-Id}",
  });
});

test("lookup exposes a missing provider and links to workspace configuration", () => {
  setup("lookup", { provider: "custom", key: "{body.record_id}" });
  expect(
    screen.getByText("This reference is missing or incompatible."),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Manage lookups" }),
  ).toBeInTheDocument();
  expect(
    screen.getByText("Result variable defaults to sample."),
  ).toBeInTheDocument();
});

test("rate-limit unrecognized imports remain visible with a repair error", () => {
  const change = setup("rate_limit", {
    rpm: 60,
    key_by: "legacy-key",
    future: "keep",
  });
  expect(
    screen.getByText(/Choose a supported identity for this stage/),
  ).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
});

test("rate-limit header choice needs a meaningful header name", () => {
  const change = setup("rate_limit", {
    rpm: 60,
    key_by: "header.",
    future: "keep",
  });
  expect(
    screen.getByText("Enter the header name."),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Rate-limit header"), {
    target: { value: "X-Client-Id" },
  });
  expect(change).toHaveBeenLastCalledWith({
    rpm: 60,
    key_by: "header.X-Client-Id",
    future: "keep",
  });
});
