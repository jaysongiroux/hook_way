import { expect, test } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { ReactFlow, ReactFlowProvider, Position } from "@xyflow/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { edgeTypes } from "./RouteEdge";
import { createNode, resetIdCounters } from "./model";
import { useGraphStore } from "./store";
import { EDGE } from "../tokens";

function hexRgb(hex: string) {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return `rgb(${r}, ${g}, ${b})`;
}

function pair() {
  resetIdCounters();
  const a = {
    ...createNode("switch", { x: 0, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "source" as const, position: Position.Right, x: 0, y: 0, width: 1, height: 1 }],
  };
  const b = {
    ...createNode("publish", { x: 200, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "target" as const, position: Position.Left, x: 0, y: 0, width: 1, height: 1 }],
  };
  return { a, b };
}

test("RouteEdge renders a labelled port as a chip", () => {
  const { a, b } = pair();
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[a, b]}
            edges={[{ id: "e1", source: a.id, target: b.id, type: "route", data: { label: "matched" } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  expect(getByText("matched")).toBeInTheDocument();
});

test("a route edge renders an arrowhead marker showing data direction", () => {
  const { a, b } = pair();
  const { container } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[a, b]}
            edges={[{ id: "e1", source: a.id, target: b.id, type: "route", markerEnd: { type: "arrowclosed" }, data: { label: "matched" } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  const path = container.querySelector(".react-flow__edge-path") as SVGPathElement;
  expect(path.getAttribute("marker-end")).toBeTruthy();
});

test("the selected edge's chip is highlighted", () => {
  useGraphStore.setState({ selectedId: null, selectedEdgeId: "e1" });
  const { a, b } = pair();
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[a, b]}
            edges={[{ id: "e1", source: a.id, target: b.id, type: "route", data: { label: "matched" } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  const chip = getByText("matched");
  expect(chip.style.borderColor).toBe(hexRgb(EDGE.selected));
  expect(chip.style.fontWeight).toBe("600");
});

test("clicking a route chip selects that edge", () => {
  useGraphStore.setState({ selectedId: null, selectedEdgeId: null });
  const { a, b } = pair();
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[a, b]}
            edges={[{ id: "e1", source: a.id, target: b.id, type: "route", data: { label: "matched" } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  fireEvent.click(getByText("matched"));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});
