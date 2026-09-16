import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { FlowNode } from "./nodes";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

function wrap(ui: React.ReactNode) {
  return render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>{ui}</ReactFlowProvider>
    </MantineProvider>,
  );
}

const nodeProps = {
  selected: false,
  dragging: false,
  zIndex: 0,
  isConnectable: true,
  positionAbsoluteX: 0,
  positionAbsoluteY: 0,
  deletable: true,
  draggable: true,
  selectable: true,
};

test("FlowNode shows id, type, and a source handle", () => {
  resetIdCounters();
  const n = createNode("http_trigger", { x: 0, y: 0 });
  const { container } = wrap(
    <FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />,
  );
  expect(screen.getByText(n.id)).toBeInTheDocument();
  expect(screen.getByText("http trigger")).toBeInTheDocument();
  expect(container.querySelector(".react-flow__handle-right")).not.toBeNull();
  expect(container.querySelector(".react-flow__handle-left")).toBeNull();
});

test("switch renders labelled output ports and on_error", () => {
  resetIdCounters();
  const n = createNode("switch", { x: 0, y: 0 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(container.querySelector('[data-handleid="matched"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="default"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="on_error"]')).not.toBeNull();
});

test("node is outlined when selected", () => {
  resetIdCounters();
  const n = createNode("filter", { x: 0, y: 0 });
  useGraphStore.setState({ selectedId: n.id, selectedEdgeId: null });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  const paper = container.querySelector(".mantine-Paper-root") as HTMLElement;
  expect(paper.style.outline).toContain("2px solid");
});
