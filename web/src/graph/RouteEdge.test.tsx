import { expect, test } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { ReactFlow, ReactFlowProvider, Position } from "@xyflow/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { edgeTypes } from "./RouteEdge";
import { createNode, resetIdCounters } from "./model";
import { useGraphStore } from "./store";

test("RouteEdge renders the target as a chip", () => {
  resetIdCounters();
  // jsdom never runs layout, so React Flow's ResizeObserver-driven node
  // measurement never fires and edges have nowhere to anchor. Supplying
  // explicit `handles` (the documented SSR/headless pattern) gives the edge
  // renderer positions without relying on DOM measurement.
  const ep = {
    ...createNode("endpoint", { x: 0, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "source" as const, position: Position.Bottom, x: 0, y: 0, width: 1, height: 1 }],
  };
  const d = {
    ...createNode("destination", { x: 200, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "target" as const, position: Position.Top, x: 0, y: 0, width: 1, height: 1 }],
  };
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[ep, d]}
            edges={[{ id: "e1", source: ep.id, target: d.id, type: "route", data: { target: "gh-webhooks", create: true } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  expect(getByText("gh-webhooks")).toBeInTheDocument();
});

test("renders both chips for two parallel routes to the same destination", () => {
  resetIdCounters();
  const ep = {
    ...createNode("endpoint", { x: 0, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "source" as const, position: Position.Bottom, x: 0, y: 0, width: 1, height: 1 }],
  };
  const d = {
    ...createNode("destination", { x: 200, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "target" as const, position: Position.Top, x: 0, y: 0, width: 1, height: 1 }],
  };
  const mk = (id: string, index: number, target: string) => ({
    id,
    source: ep.id,
    target: d.id,
    type: "route",
    data: { target, create: true, parallelIndex: index, parallelCount: 2 },
  });
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[ep, d]}
            edges={[mk("e1", 0, "queue-a"), mk("e2", 1, "queue-b")]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  expect(getByText("queue-a")).toBeInTheDocument();
  expect(getByText("queue-b")).toBeInTheDocument();
});

test("the selected edge's chip is highlighted", () => {
  resetIdCounters();
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: "e1" });
  const ep = {
    ...createNode("endpoint", { x: 0, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "source" as const, position: Position.Bottom, x: 0, y: 0, width: 1, height: 1 }],
  };
  const d = {
    ...createNode("destination", { x: 200, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "target" as const, position: Position.Top, x: 0, y: 0, width: 1, height: 1 }],
  };
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[ep, d]}
            edges={[{ id: "e1", source: ep.id, target: d.id, type: "route", data: { target: "gh", create: true } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  const chip = getByText("gh");
  expect(chip.style.borderColor).toBe("rgb(54, 108, 237)"); // #366CED, jsdom-normalized
  expect(chip.style.fontWeight).toBe("600");
});

test("clicking a route chip selects that edge", () => {
  resetIdCounters();
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: null });
  const ep = {
    ...createNode("endpoint", { x: 0, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "source" as const, position: Position.Bottom, x: 0, y: 0, width: 1, height: 1 }],
  };
  const d = {
    ...createNode("destination", { x: 200, y: 0 }),
    width: 100,
    height: 40,
    handles: [{ type: "target" as const, position: Position.Top, x: 0, y: 0, width: 1, height: 1 }],
  };
  const { getByText } = render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>
        <div style={{ width: 600, height: 400 }}>
          <ReactFlow
            nodes={[ep, d]}
            edges={[{ id: "e1", source: ep.id, target: d.id, type: "route", data: { target: "gh", create: true } }]}
            edgeTypes={edgeTypes}
          />
        </div>
      </ReactFlowProvider>
    </MantineProvider>,
  );
  fireEvent.click(getByText("gh"));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});
