import { useState } from "react";
import { expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider, TextInput } from "@mantine/core";
import { BatchChildEditor, batchChildIssues } from "./BatchChildEditor";
import { useGraphStore } from "./store";

const send = {
  id: "batch.send",
  type: "publish",
  destination: "primary",
  target: "events",
};
const process = {
  id: "batch.map",
  type: "transform",
  kind: "strip_fields",
  fields: ["private"],
};
const valid = {
  entry: "batch.map",
  nodes: [process, send],
  edges: ["batch.map>batch.send"],
};

function setup(value: unknown, parentId = "batch") {
  const change = vi.fn();
  function Controlled() {
    const [current, setCurrent] = useState(value);
    return (
      <MantineProvider>
        <BatchChildEditor
          value={current}
          parentId={parentId}
          scope="test-child"
          onChange={(next) => {
            change(next);
            setCurrent(next);
          }}
          renderNodeOptions={(type, options, onChange) => (
            <TextInput
              label={`${type} example option`}
              value={String(options.example ?? "")}
              onChange={(event) =>
                onChange({ ...options, example: event.currentTarget.value })
              }
            />
          )}
        />
      </MantineProvider>
    );
  }
  render(<Controlled />);
  return change;
}

test("catalog-aware children expose the permitted types and delegate option editing", () => {
  const change = setup(valid);
  expect(
    screen.queryByText("Child flow needs attention"),
  ).not.toBeInTheDocument();
  const select = screen.getByLabelText("New child node type");
  expect(
    [...select.querySelectorAll("option")].map((option) => option.value),
  ).toEqual(["transform", "code", "filter", "if_else", "switch", "publish"]);
  fireEvent.change(screen.getByLabelText("transform example option"), {
    target: { value: "authored" },
  });
  expect(change).toHaveBeenLastCalledWith({
    ...valid,
    nodes: [{ ...process, example: "authored" }, send],
  });
});

test("ID changes keep entry and structured edge references in sync", () => {
  const change = setup(valid);
  fireEvent.change(screen.getByLabelText("Child node 1 ID"), {
    target: { value: "batch.changed" },
  });
  expect(change).toHaveBeenLastCalledWith({
    ...valid,
    entry: "batch.changed",
    nodes: [{ ...process, id: "batch.changed" }, send],
    edges: ["batch.changed>batch.send"],
  });
  expect(screen.getByLabelText("Edge 1 source")).toHaveValue("batch.changed");
});

test("exactly one publish is required and adding a second publish is disabled", () => {
  setup(valid);
  fireEvent.change(screen.getByLabelText("New child node type"), {
    target: { value: "publish" },
  });
  expect(screen.getByRole("button", { name: "Add child node" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Remove child node 2" }));
  expect(
    screen.getByText("A child flow must contain exactly one publish node."),
  ).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add child node" })).toBeEnabled();
});

test("added children use scoped IDs and changing type replaces incompatible options deliberately", () => {
  const change = setup({ entry: "batch.send", nodes: [send], edges: [] });
  fireEvent.change(screen.getByLabelText("New child node type"), {
    target: { value: "filter" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add child node" }));
  const next = change.mock.calls.at(-1)?.[0] as {
    nodes: Record<string, unknown>[];
  };
  expect(next.nodes[1]).toMatchObject({ id: "batch.filter", type: "filter" });
  expect(
    screen.getAllByText(/Changing the node type replaces its options/),
  ).toHaveLength(2);
  fireEvent.change(screen.getByLabelText("Child node 2 type"), {
    target: { value: "code" },
  });
  const switched = change.mock.calls.at(-1)?.[0] as {
    nodes: Record<string, unknown>[];
  };
  expect(switched.nodes[1].type).toBe("code");
  expect(switched.nodes[1]).not.toHaveProperty("conditions");
});

test("unsupported imported nodes and malformed edges are retained until explicitly repaired", () => {
  const imported = {
    entry: "batch.bad",
    nodes: [{ id: "batch.bad", type: "batch", custom: "keep" }, send],
    edges: ["malformed"],
  };
  const change = setup(imported);
  expect(
    screen.getByText(
      /unsupported imported node is retained until changed or removed/,
    ),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/Edge 1 has an unsupported format and is retained/),
  ).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Child entry node"), {
    target: { value: "batch.send" },
  });
  expect(change).toHaveBeenLastCalledWith({ ...imported, entry: "batch.send" });
  fireEvent.click(screen.getByRole("button", { name: "Replace edge 1" }));
  fireEvent.change(screen.getByLabelText("Edge 1 source"), {
    target: { value: "batch.bad" },
  });
  fireEvent.change(screen.getByLabelText("Edge 1 target"), {
    target: { value: "batch.send" },
  });
  expect(change.mock.calls.at(-1)?.[0].edges).toEqual(["batch.bad>batch.send"]);
});

test("malformed imported node lists are not overwritten by Add", () => {
  const change = setup({ entry: "", nodes: { future: true }, edges: [] });
  expect(screen.getByRole("button", { name: "Add child node" })).toBeDisabled();
  fireEvent.click(
    screen.getByRole("button", { name: "Replace imported node list" }),
  );
  expect(change).toHaveBeenLastCalledWith({ entry: "", nodes: [], edges: [] });
});

test("child readiness covers prefix, unique IDs, cycles, unreachable nodes, and invalid ports", () => {
  expect(batchChildIssues(valid, "batch")).toEqual([]);
  expect(batchChildIssues(valid, "other")).toContain(
    "Every child node ID must start with other. followed by a name.",
  );
  expect(
    batchChildIssues(
      { ...valid, nodes: [process, send, { ...send }] },
      "batch",
    ),
  ).toContain("Child node IDs must be unique.");
  expect(
    batchChildIssues(
      { ...valid, edges: ["batch.map>batch.map", "batch.map>batch.send"] },
      "batch",
    ),
  ).toContain("Child flow connections must not contain a cycle.");
  expect(batchChildIssues({ ...valid, edges: [] }, "batch")).toContain(
    "Every child node must be reachable from the entry node.",
  );
  expect(
    batchChildIssues(
      { ...valid, edges: ["batch.map:made_up>batch.send"] },
      "batch",
    ),
  ).toContain("Edge 1 selects an output port this node does not expose.");
});

test("removing a preceding child retains the later row DOM identity", () => {
  setup(valid);
  const later = screen.getByLabelText("Child node 2 ID");
  fireEvent.click(screen.getByRole("button", { name: "Remove child node 1" }));
  expect(screen.getByLabelText("Child node 1 ID")).toBe(later);
});

test("invalid nested JSON drafts follow their child across preceding removal and reopening", () => {
  useGraphStore.getState().clear();
  let last: unknown = {
    entry: "batch.map",
    nodes: [
      process,
      {
        id: "batch.schema",
        type: "transform",
        kind: "json_schema",
        json_schema: { type: "object" },
      },
      send,
    ],
    edges: ["batch.map>batch.schema", "batch.schema>batch.send"],
  };
  function Controlled() {
    const [current, setCurrent] = useState(last);
    return (
      <MantineProvider>
        <BatchChildEditor
          value={current}
          parentId="batch"
          scope="persistent-child"
          onChange={(next) => {
            last = next;
            setCurrent(next);
          }}
        />
      </MantineProvider>
    );
  }
  const first = render(<Controlled />);
  fireEvent.change(screen.getByLabelText("json schema"), {
    target: { value: '{"type":' },
  });
  fireEvent.click(screen.getByRole("button", { name: "Remove child node 1" }));
  first.unmount();
  render(<Controlled />);
  expect(screen.getByLabelText("json schema")).toHaveValue('{"type":');
  expect(
    Object.values(useGraphStore.getState().formDrafts ?? {}).some(
      (draft) => draft.error,
    ),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Remove child node 1" }));
  expect(
    Object.values(useGraphStore.getState().formDrafts ?? {}).some(
      (draft) => draft.error,
    ),
  ).toBe(false);
});
