import { useState } from "react";
import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { ProcessingOptionsForm } from "./ProcessingOptionsForm";
import { useGraphStore } from "./store";

beforeEach(() => useGraphStore.getState().clear());
function setup(nodeType: string, value: Record<string, unknown>) {
  const change = vi.fn();
  function Controlled() {
    const [current, setCurrent] = useState(value);
    return (
      <MantineProvider>
        <ProcessingOptionsForm
          nodeType={nodeType}
          value={current}
          scope="processing-test"
          nodeId="batch"
          onChange={(next) => {
            setCurrent(next);
            change(next);
          }}
          renderNodeOptions={() => null}
        />
      </MantineProvider>
    );
  }
  render(<Controlled />);
  return change;
}

test("transform operation shows only its active fields and supplies conditional requirements", () => {
  const change = setup("transform", {
    kind: "changed_fields",
    from: "body.old",
    to: "body.new",
    name: "diff",
  });
  expect(screen.getByLabelText("First value path")).toBeRequired();
  expect(screen.getByLabelText("Second value path")).toBeRequired();
  expect(screen.queryByText("xml adapter")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("json schema")).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Transform operation"), {
    target: { value: "strip_fields" },
  });
  expect(change).toHaveBeenLastCalledWith({ kind: "strip_fields" });
  expect(screen.queryByLabelText("First value path")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Fields to remove")).toBeRequired();
  expect(screen.getByText(/Required to configure/)).toBeInTheDocument();
});

test("imported inactive and unknown transform options remain until explicit removal", () => {
  const imported = {
    kind: "field_map",
    mappings: [{ from: "body.a", to: "body.b" }],
    xml_adapter: { link: { local: "link" } },
    custom: "keep",
  };
  const change = setup("transform", imported);
  expect(change).not.toHaveBeenCalled();
  expect(screen.getByText(/xml_adapter, custom/)).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Remove inapplicable imported options",
    }),
  );
  expect(change).toHaveBeenLastCalledWith({
    kind: "field_map",
    mappings: imported.mappings,
  });
});

test("transform switching preserves unknown imports and overlapping authored fields", () => {
  const change = setup("transform", {
    kind: "changed_fields",
    from: "body.old",
    to: "body.new",
    custom: "review",
  });
  fireEvent.change(screen.getByLabelText("Transform operation"), {
    target: { value: "changed_fields_js" },
  });
  expect(change).toHaveBeenLastCalledWith({
    kind: "changed_fields_js",
    from: "body.old",
    to: "body.new",
    custom: "review",
  });
  expect(
    screen.getByText(/Both paths must begin with body/),
  ).toBeInTheDocument();
});

test("JSON parser omission shows bounded defaults without serializing them", () => {
  const change = setup("envelope", {});
  expect(screen.getByLabelText("Maximum JSON depth")).toHaveAttribute(
    "placeholder",
    "Default: 128",
  );
  expect(screen.getByLabelText("Maximum JSON values")).toHaveAttribute(
    "placeholder",
    "Default: 1000000",
  );
  expect(
    screen.getByText(/separately caps the original request bytes/),
  ).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
  expect(screen.queryByText(/fields are retained/)).not.toBeInTheDocument();
});

test("imported JSON limits remain editable and reset to the runtime defaults", () => {
  const change = setup("envelope", {
    json: { max_depth: 512, max_nodes: 4000 },
  });
  expect(screen.getByLabelText("Maximum JSON depth")).toHaveValue("512");
  expect(screen.getByLabelText("Maximum JSON values")).toHaveValue("4000");
  expect(screen.queryByText(/fields are retained/)).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Maximum JSON depth"), {
    target: { value: "1024" },
  });
  expect(change).toHaveBeenLastCalledWith({
    json: { max_depth: 1024, max_nodes: 4000 },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Use default json parser limits" }),
  );
  expect(change).toHaveBeenLastCalledWith({});
  expect(screen.getByLabelText("Maximum JSON depth")).toHaveAttribute(
    "placeholder",
    "Default: 128",
  );
});

test("switching away from JSON removes only its known parser options", () => {
  const change = setup("envelope", {
    json: { max_depth: 512 },
    custom: "review",
  });
  fireEvent.change(screen.getByLabelText("Body format"), {
    target: { value: "raw" },
  });
  expect(change).toHaveBeenLastCalledWith({
    body_format: "raw",
    custom: "review",
  });
  expect(screen.queryByLabelText("Maximum JSON depth")).not.toBeInTheDocument();
});

test("multipart omission displays the true content default without serializing it", () => {
  const change = setup("envelope", { body_format: "multipart" });
  expect(screen.getByLabelText("Include file content")).toBeChecked();
  expect(change).not.toHaveBeenCalled();
  fireEvent.click(screen.getByLabelText("Include file content"));
  expect(change).toHaveBeenLastCalledWith({
    body_format: "multipart",
    multipart: { include_content: false },
  });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Use default for Include file content",
    }),
  );
  expect(change).toHaveBeenLastCalledWith({
    body_format: "multipart",
    multipart: {},
  });
  expect(screen.getByLabelText("Include file content")).toBeChecked();
});

test("envelope format switching removes the previous parser and opening omitted format stays omitted", () => {
  const change = setup("envelope", { xml: { max_bytes: 4096 } });
  expect(screen.getByLabelText("Body format")).toHaveValue("json");
  expect(change).not.toHaveBeenCalled();
  expect(screen.getByText(/fields are retained/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Body format"), {
    target: { value: "multipart" },
  });
  expect(change).toHaveBeenLastCalledWith({ body_format: "multipart" });
  expect(screen.queryByLabelText("Maximum XML bytes")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Maximum parts")).toBeInTheDocument();
});

test("dedicated batch offers environments admitted for both its profile and account", () => {
  useGraphStore.getState().setConfigExtra({
    routing: {
      profiles: {
        sandbox: { account_id: "000000000000", credential_mode: "sandbox" },
      },
      environments: {
        preview: {
          profiles: ["sandbox"],
          accounts: ["000000000000"],
          aliases: ["staging"],
        },
        blocked: { profiles: ["sandbox"], accounts: ["111111111111"] },
      },
    },
  });
  const change = setup("batch", {
    mode: "dedicated",
    profile: "sandbox",
    receiving_environment: "preview",
    environment_path: "environment",
    event_id_path: "id",
    child: {
      entry: "batch.send",
      nodes: [{ id: "batch.send", type: "publish" }],
      edges: [],
    },
  });
  expect(
    [
      ...screen
        .getByLabelText("Receiving environment")
        .querySelectorAll("option"),
    ].map((option) => option.value),
  ).toEqual(["preview", "staging"]);
  expect(
    screen.queryByLabelText("Group ID field path"),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Batch mode"), {
    target: { value: "shared" },
  });
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty(
    "receiving_environment",
  );
  expect(screen.queryByLabelText("Concurrent events")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Group ID field path")).toBeInTheDocument();
});

test("shared batch imports retain conflicting fields until a mode choice cleans them", () => {
  const change = setup("batch", {
    mode: "shared",
    profile: "missing",
    receiving_environment: "preview",
    concurrency: 8,
    group_id_path: "group",
    child: { entry: "", nodes: [], edges: [] },
  });
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByText(/Shared batches require concurrency 1/),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Batch mode"), {
    target: { value: "dedicated" },
  });
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("group_id_path");
  expect(change.mock.calls.at(-1)?.[0]).toHaveProperty("concurrency", 8);
});

test("if/else omitted match displays all and condition controls write a typed scalar", () => {
  const change = setup("if_else", {
    conditions: [
      { source: "body", path: "active", operator: "eq", value: false },
    ],
  });
  expect(screen.getByLabelText("Match conditions")).toHaveValue("all");
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Condition 1 Value"), {
    target: { value: "true" },
  });
  expect(change).toHaveBeenLastCalledWith({
    conditions: [
      { source: "body", path: "active", operator: "eq", value: true },
    ],
  });
});

test("batch timeout feedback rejects zero and values above two minutes", () => {
  setup("batch", {
    mode: "shared",
    profile: "sandbox",
    environment_path: "environment",
    event_id_path: "id",
    timeout: "3m",
    child: {
      entry: "batch.send",
      nodes: [{ id: "batch.send", type: "publish" }],
      edges: [],
    },
  });
  expect(
    screen.getByText(
      "Enter a positive duration no longer than 2m, such as 30s.",
    ),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Timeout"), {
    target: { value: "1m30s" },
  });
  expect(
    screen.queryByText(
      "Enter a positive duration no longer than 2m, such as 30s.",
    ),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Timeout"), {
    target: { value: "0s" },
  });
  expect(
    screen.getByText(
      "Enter a positive duration no longer than 2m, such as 30s.",
    ),
  ).toBeInTheDocument();
});
