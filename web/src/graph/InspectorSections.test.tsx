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

test("primary node options are direct and identity metadata starts collapsed", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [n],
    edges: [],
    selectedId: n.id,
    selectedEdgeId: null,
  });
  wrap();
  const path = screen.getByLabelText("Request path");
  const metadata = section("Identity and description");
  expect(path).toBeInTheDocument();
  expect(screen.queryByLabelText("Id")).not.toBeInTheDocument();
  expect(metadata).toHaveAttribute("aria-expanded", "false");
  expect(
    path.compareDocumentPosition(metadata) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
});

test("Identity and description opens below primary options when requested", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [n],
    edges: [],
    selectedId: n.id,
    selectedEdgeId: null,
  });
  wrap();
  fireEvent.click(section("Identity and description"));
  fireEvent.change(screen.getByLabelText("Node ID"), { target: { value: "in" } });
  fireEvent.change(screen.getByLabelText("Description"), {
    target: { value: "Entry point." },
  });
  expect(useGraphStore.getState().nodes[0].data.yamlId).toBe("in");
  expect(useGraphStore.getState().nodes[0].data.description).toBe(
    "Entry point.",
  );
});

test("sections open the first group by default", () => {
  render(
    <MantineProvider theme={theme}>
      <Sections
        sections={[
          { value: "a", label: "Alpha", render: () => <p>alpha body</p> },
        ]}
      />
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
