import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { FlowNode } from "./nodes";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";
import { NODE_SIZE } from "../tokens";
import { NODE_ICON_SIZE } from "./nodeIcon";

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
  expect(screen.getByText("HTTP endpoint")).toBeInTheDocument();
  const icon = container.querySelector('[data-node-icon="http_trigger"] svg');
  expect(icon).not.toBeNull();
  expect(icon).toHaveAttribute("width", String(NODE_ICON_SIZE));
  expect(icon).toHaveAttribute("height", String(NODE_ICON_SIZE));
  expect(container.querySelector(".react-flow__handle-right")).not.toBeNull();
  expect(container.querySelector(".react-flow__handle-left")).toBeNull();
});

test("every node icon uses the same color", () => {
  resetIdCounters();
  const filter = createNode("filter", { x: 0, y: 0 });
  const publish = createNode("publish", { x: 0, y: 0 });
  publish.data.options = { destination: "orders", target: "q" };
  useGraphStore.setState({ destinations: { orders: { type: "sqs" } } });
  const { container } = wrap(
    <>
      <FlowNode id={filter.id} type="flow" data={filter.data} {...nodeProps} />
      <FlowNode id={publish.id} type="flow" data={publish.data} {...nodeProps} />
    </>,
  );
  const colors = [...container.querySelectorAll("[data-node-icon]")].map((el) => (el as HTMLElement).style.color);
  expect(colors).toHaveLength(2);
  expect(colors[0]).toBe(colors[1]);
  expect(colors[0]).not.toBe("");
});

test("a publish node shows the icon of its destination", () => {
  resetIdCounters();
  const n = createNode("publish", { x: 0, y: 0 });
  n.data.options = { destination: "orders", target: "github-main" };
  useGraphStore.setState({ destinations: { orders: { type: "sqs" }, events: { type: "kafka" } } });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(container.querySelector('[data-node-icon="sqs"] svg')).not.toBeNull();
  expect(container.querySelector('[data-node-icon="publish"]')).toBeNull();
});

test("switch renders labelled output ports and an on-error handle", () => {
  resetIdCounters();
  const n = createNode("switch", { x: 0, y: 0 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(container.querySelector('[data-handleid="matched"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="default"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="on_error"]')).not.toBeNull();
  expect(screen.getByText("on error")).toBeInTheDocument();
});

test("labelled ports are named on the card, not just on hover", () => {
  resetIdCounters();
  const n = createNode("switch", { x: 0, y: 0 });
  wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  // A bare dot gives no clue which branch it is; the name has to be visible.
  expect(screen.getByText("matched")).toBeInTheDocument();
  expect(screen.getByText("default")).toBeInTheDocument();
});

test("sample starts at keep-all and has no on-error handle", () => {
  resetIdCounters();
  const n = createNode("sample", { x: 0, y: 0 });
  expect(n.data.options).toEqual({ rate: 1 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(screen.getByText("Sample requests")).toBeInTheDocument();
  expect(screen.getByText("100% kept")).toBeInTheDocument();
  expect(container.querySelector('[data-handleid="out"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="on_error"]')).toBeNull();
});

test("if_else renders true and false ports and no on-error handle", () => {
  resetIdCounters();
  const n = createNode("if_else", { x: 0, y: 0 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(container.querySelector('[data-handleid="true"]')).not.toBeNull();
  expect(container.querySelector('[data-handleid="false"]')).not.toBeNull();
  expect(screen.getByText("true")).toBeInTheDocument();
  expect(screen.getByText("false")).toBeInTheDocument();
  // An if_else cannot fail, so an on_error handle would be dead wiring.
  expect(container.querySelector('[data-handleid="on_error"]')).toBeNull();
  // Labelled ports replace the default out handle rather than joining it.
  expect(container.querySelector('[data-handleid="out"]')).toBeNull();
});

test("a long port name is truncated on the card and kept on hover", () => {
  resetIdCounters();
  const n = createNode("switch", { x: 0, y: 0 });
  const port = "refs/heads/release-candidate-2026";
  n.data.options = { cases: [{ port, when: { source: "body", path: "ref", operator: "eq" } }], default: "other" };
  wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  const label = screen.getByText(port);
  // The card is a fixed width, so the name is clipped rather than allowed to
  // widen the node past what layout.ts reserved for it.
  expect(label).toHaveAttribute("title", port);
  expect(label.style.textOverflow).toBe("ellipsis");
  expect(label.style.overflow).toBe("hidden");
  expect(label.style.maxWidth).toBe(`${NODE_SIZE.width - 2 * NODE_SIZE.padding - NODE_SIZE.handle}px`);
});

test("http_trigger has no on-error handle", () => {
  resetIdCounters();
  const n = createNode("http_trigger", { x: 0, y: 0 });
  wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(screen.queryByText("on error")).not.toBeInTheDocument();
});

test("read_body and no_auth have no on-error handle", () => {
  resetIdCounters();
  wrap(<FlowNode id="read_body_1" type="flow" data={createNode("read_body", { x: 0, y: 0 }).data} {...nodeProps} />);
  expect(screen.queryByText("on error")).not.toBeInTheDocument();
  wrap(<FlowNode id="no_auth_1" type="flow" data={createNode("no_auth", { x: 0, y: 0 }).data} {...nodeProps} />);
  expect(screen.queryByText("on error")).not.toBeInTheDocument();
});

test("rate_limit keeps an on-error handle", () => {
  resetIdCounters();
  const n = createNode("rate_limit", { x: 0, y: 0 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  expect(container.querySelector('[data-handleid="on_error"]')).not.toBeNull();
  expect(screen.getByText("on error")).toBeInTheDocument();
});

test("node is outlined when selected", () => {
  resetIdCounters();
  const n = createNode("filter", { x: 0, y: 0 });
  useGraphStore.setState({ selectedId: n.id, selectedEdgeId: null });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  const paper = container.querySelector(".mantine-Paper-root") as HTMLElement;
  expect(paper.style.outline).toContain("2px solid");
});

test("FlowNode renders the description on the card", () => {
  resetIdCounters();
  const n = createNode("auth", { x: 0, y: 0 });
  n.data.description = "Verify the GitHub signature over the raw body.";
  wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  const text = screen.getByText("Verify the GitHub signature over the raw body.");
  expect(text).toBeInTheDocument();
  // Clamped to two lines, so the full text has to stay reachable on hover.
  expect(text).toHaveAttribute("title", "Verify the GitHub signature over the raw body.");
});

test("FlowNode omits the description line when there is none", () => {
  resetIdCounters();
  const n = createNode("auth", { x: 0, y: 0 });
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  // Scoped to card text: the port handles carry their own title attributes, and
  // the summary line carries one because it ellipsises (see below).
  const titled = [...container.querySelectorAll("p[title]")].map((el) => el.getAttribute("title"));
  expect(titled).toEqual(["Verify signature or token", "hmac_sha256_hex"]);
});

test("an ellipsised summary stays readable on hover", () => {
  resetIdCounters();
  const n = createNode("http_request", { x: 0, y: 0 });
  const url = "https://hooks.slack.test/services/T0000000/B0000000/averylongsecrettoken";
  n.data.options = { ...n.data.options, url };
  wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  // The card is a fixed NODE_SIZE width, so a URL this long is cut off on screen. The
  // full value has to remain reachable without opening the Inspector.
  expect(screen.getByText(url)).toHaveAttribute("title", url);
});

test("the card renders at the shared NODE_SIZE width so the layout estimate is exact", () => {
  resetIdCounters();
  const n = createNode("read_body", { x: 0, y: 0 });
  n.data.description = "Reads the raw body, rejecting anything over 1 MiB.";
  const { container } = wrap(<FlowNode id={n.id} type="flow" data={n.data} {...nodeProps} />);
  const paper = container.querySelector(".mantine-Paper-root") as HTMLElement;
  // A card that sizes to its text makes layout.ts's dagre estimate a lie, which
  // is what bunched the nodes together on import and Arrange.
  expect(paper.style.width).toBe(`${NODE_SIZE.width}px`);
  expect(paper.style.minWidth).toBe("");
});
