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

function section(name: string) {
  return screen.getByRole("button", { name: new RegExp(`^${name}`, "i") });
}

beforeEach(() => {
  resetIdCounters();
  useGraphStore.getState().clear();
});

test("a selected node opens on Options with Notes collapsed", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id, selectedEdgeId: null });
  wrap();
  expect(section("Options")).toHaveAttribute("aria-expanded", "true");
  expect(section("Notes")).toHaveAttribute("aria-expanded", "false");
  expect(screen.getByLabelText("path")).toBeInTheDocument();
});

test("the id stays outside the sections so it is always editable", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id, selectedEdgeId: null });
  wrap();
  fireEvent.change(screen.getByLabelText("Id"), { target: { value: "in" } });
  expect(useGraphStore.getState().nodes[0].data.yamlId).toBe("in");
});
