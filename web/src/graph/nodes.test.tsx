import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { EndpointNode, DestinationNode, FilterNode } from "./nodes";
import { useGraphStore } from "./store";
import type { EndpointData, DestinationData, FilterData } from "./model";

function wrap(ui: React.ReactNode) {
  return render(
    <MantineProvider theme={theme}>
      <ReactFlowProvider>{ui}</ReactFlowProvider>
    </MantineProvider>,
  );
}

const endpointData: EndpointData = {
  kind: "endpoint",
  name: "endpoint_1",
  path: "/webhooks/gh",
  enabled: true,
  auth: {
    type: "hmac_sha256",
    scheme: "hmac_sha256_hex",
    secret: "",
    secretMode: "env",
    signatureHeader: "X-Signature-256",
  },
};
const destData: DestinationData = {
  kind: "destination",
  name: "destination_1",
  destType: "rabbitmq",
  settings: { url: "", exchange: "", exchange_type: "direct" },
};

test("EndpointNode shows name and path and has a source handle", () => {
  const { container } = wrap(
    <EndpointNode id="endpoint_1" type="endpoint" data={endpointData} selected={false}
      dragging={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} deletable draggable selectable />,
  );
  expect(screen.getByText("endpoint_1")).toBeInTheDocument();
  expect(screen.getByText("/webhooks/gh")).toBeInTheDocument();
  expect(container.querySelector(".react-flow__handle-right")).not.toBeNull();
  expect(container.querySelector(".react-flow__handle-left")).toBeNull();
});

test("DestinationNode shows name and type and has a target handle", () => {
  const { container } = wrap(
    <DestinationNode id="destination_1" type="destination" data={destData} selected={false}
      dragging={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} deletable draggable selectable />,
  );
  expect(screen.getByText("destination_1")).toBeInTheDocument();
  expect(screen.getByText("rabbitmq")).toBeInTheDocument();
  expect(container.querySelector(".react-flow__handle-left")).not.toBeNull();
  expect(container.querySelector(".react-flow__handle-right")).toBeNull();
});

test("node is outlined when it is the selected node", () => {
  useGraphStore.setState({ selectedId: "endpoint_1", selectedEdgeId: null });
  const { container } = wrap(
    <EndpointNode id="endpoint_1" type="endpoint" data={endpointData} selected={false}
      dragging={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} deletable draggable selectable />,
  );
  const paper = container.querySelector(".mantine-Paper-root") as HTMLElement;
  expect(paper.style.outline).toContain("2px solid");
});

test("node is not outlined when a different node is selected", () => {
  useGraphStore.setState({ selectedId: "other", selectedEdgeId: null });
  const { container } = wrap(
    <EndpointNode id="endpoint_1" type="endpoint" data={endpointData} selected={false}
      dragging={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} deletable draggable selectable />,
  );
  const paper = container.querySelector(".mantine-Paper-root") as HTMLElement;
  expect(paper.style.outline).toBe("");
});

const filterData: FilterData = { kind: "filter", name: "filter_1", source: "body", path: "action", operator: "eq", value: "opened" };

test("FilterNode shows name + condition summary and has BOTH handles", () => {
  const { container } = wrap(
    <FilterNode id="filter_1" type="filter" data={filterData} selected={false}
      dragging={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} deletable draggable selectable />,
  );
  expect(screen.getByText("filter_1")).toBeInTheDocument();
  expect(screen.getByText(/action/)).toBeInTheDocument();
  expect(container.querySelector(".react-flow__handle-left")).not.toBeNull();
  expect(container.querySelector(".react-flow__handle-right")).not.toBeNull();
});
