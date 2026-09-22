import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Inspector } from "./Inspector";
import { Sections } from "./InspectorSections";
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

test("a selected node opens on Options", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id, selectedEdgeId: null });
  wrap();
  expect(section("Options")).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByLabelText("path")).toBeInTheDocument();
});

test("id and description stay outside the sections so both are always editable", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id, selectedEdgeId: null });
  wrap();
  fireEvent.change(screen.getByLabelText("Id"), { target: { value: "in" } });
  fireEvent.change(screen.getByLabelText("Description"), { target: { value: "Entry point." } });
  expect(useGraphStore.getState().nodes[0].data.yamlId).toBe("in");
  expect(useGraphStore.getState().nodes[0].data.description).toBe("Entry point.");
});

test("sections open the first group by default", () => {
  render(
    <MantineProvider theme={theme}>
      <Sections sections={[{ value: "a", label: "Alpha", render: () => <p>alpha body</p> }]} />
    </MantineProvider>,
  );
  expect(section("Alpha")).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByText("alpha body")).toBeInTheDocument();
});

test("defaultOpen lets every section start closed", () => {
  render(
    <MantineProvider theme={theme}>
      <Sections
        defaultOpen={[]}
        sections={[
          { value: "a", label: "Alpha", render: () => <p>alpha body</p> },
          { value: "b", label: "Beta", render: () => <p>beta body</p> },
        ]}
      />
    </MantineProvider>,
  );
  expect(section("Alpha")).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByText("alpha body")).not.toBeInTheDocument();

  fireEvent.click(section("Beta"));

  expect(screen.getByText("beta body")).toBeInTheDocument();
});
