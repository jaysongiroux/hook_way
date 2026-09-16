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

test("shows flow options when nothing is selected", () => {
  wrap();
  expect(screen.getByText(/select a node/i)).toBeInTheDocument();
  expect(screen.getByLabelText("Flow name")).toBeInTheDocument();
});

test("editing a trigger path updates the store", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap();
  const pathInput = screen.getByLabelText("path") as HTMLInputElement;
  fireEvent.change(pathInput, { target: { value: "/webhooks/github" } });
  expect(useGraphStore.getState().nodes[0].data.options.path).toBe("/webhooks/github");
});

test("editing notes updates the store", () => {
  const n = createNode("publish", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap();
  fireEvent.click(screen.getByRole("button", { name: /^Notes/i }));
  fireEvent.change(screen.getByRole("textbox", { name: "Notes" }), { target: { value: "audit" } });
  expect(useGraphStore.getState().nodes[0].data.notes).toBe("audit");
});
