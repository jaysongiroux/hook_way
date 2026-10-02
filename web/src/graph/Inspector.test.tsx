import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Inspector } from "./Inspector";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Inspector />
    </MantineProvider>,
  );
}

beforeEach(() => {
  resetIdCounters();
  useGraphStore.getState().clear();
});

test("flow guidance explains double-click editing", () => {
  wrap();
  expect(
    screen.getByText(/double-click a node to configure it/i),
  ).toBeInTheDocument();
  expect(screen.getByLabelText("Flow name")).toBeInTheDocument();
});

test("lists nodes in the flow so they can be opened from the inspector", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: null });
  wrap();
  fireEvent.click(
    screen.getByRole("button", { name: `Select ${n.data.yamlId}` }),
  );
  expect(useGraphStore.getState().selectedId).toBe(n.id);
});

// Leaving a node is the Config menu's "Flow settings" item now -- see
// App.test.tsx. "Back to flow" read like it dismissed the whole inspector, so
// neither the node nor the edge view offers it.
test("a selected node offers no way back that looks like dismissing the inspector", () => {
  const n = createNode("publish", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap();
  expect(
    screen.queryByRole("button", { name: "Back to flow" }),
  ).not.toBeInTheDocument();
});

test("a selected edge offers no way back that looks like dismissing the inspector", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 200, y: 0 });
  useGraphStore.setState({
    nodes: [a, b],
    edges: [{ id: "e1", source: a.id, target: b.id }],
    selectedId: null,
    selectedEdgeId: "e1",
  });
  wrap();
  expect(
    screen.queryByRole("button", { name: "Back to flow" }),
  ).not.toBeInTheDocument();
});

test("the flow name field can be emptied while typing and restores on blur if left blank", () => {
  wrap();
  const input = screen.getByLabelText("Flow name") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "" } });
  expect(input.value).toBe("");
  expect(useGraphStore.getState().activeFlow).toBe("main");
  fireEvent.blur(input);
  expect(input.value).toBe("main");
});

test("renames the flow as you type", () => {
  wrap();
  const input = screen.getByLabelText("Flow name") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "billing" } });
  expect(useGraphStore.getState().activeFlow).toBe("billing");
  expect(input.value).toBe("billing");
});

test("rejects a name that already belongs to another flow", () => {
  useGraphStore.getState().addFlow("billing");
  wrap();
  const input = screen.getByLabelText("Flow name") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "main" } });
  expect(useGraphStore.getState().activeFlow).toBe("billing");
  expect(input).toHaveAttribute("aria-invalid", "true");
  fireEvent.blur(input);
  expect(input.value).toBe("billing");
});

test("editing a trigger path updates the store", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap();
  expect(screen.getByText(/Start the flow/i)).toBeInTheDocument();
  const pathInput = screen.getByLabelText("Request path") as HTMLInputElement;
  fireEvent.change(pathInput, { target: { value: "/webhooks/github" } });
  expect(useGraphStore.getState().nodes[0].data.options.path).toBe(
    "/webhooks/github",
  );
});

test("editing the description updates the store", () => {
  const n = createNode("publish", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap();
  fireEvent.click(
    screen.getByRole("button", { name: "Identity and description" }),
  );
  fireEvent.change(screen.getByLabelText("Description"), {
    target: { value: "Fan out to billing." },
  });
  expect(useGraphStore.getState().nodes[0].data.description).toBe(
    "Fan out to billing.",
  );
});
