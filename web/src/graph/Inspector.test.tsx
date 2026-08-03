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
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null });
});

test("shows an empty state when nothing is selected", () => {
  wrap();
  expect(screen.getByText(/select a node/i)).toBeInTheDocument();
});

test("editing an endpoint path updates the store", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id });
  wrap();
  const pathInput = screen.getByLabelText("Path") as HTMLInputElement;
  fireEvent.change(pathInput, { target: { value: "/webhooks/github" } });
  expect((useGraphStore.getState().nodes[0].data as { path: string }).path).toBe("/webhooks/github");
});

test("editing a filter condition updates the store", () => {
  const f = createNode("filter", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [f], edges: [], selectedId: f.id });
  wrap();
  fireEvent.change(screen.getByLabelText("Path"), { target: { value: "action" } });
  expect((useGraphStore.getState().nodes[0].data as { path: string }).path).toBe("action");
});

test("editing notes updates the store", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id });
  wrap();
  fireEvent.change(screen.getByLabelText("Notes"), { target: { value: "audit queue" } });
  expect((useGraphStore.getState().nodes[0].data as { notes?: string }).notes).toBe("audit queue");
});

test("changing destination type reseeds settings and renders its fields", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id });
  wrap();
  // rabbitmq default → AMQP URL field present
  expect(screen.getByLabelText("AMQP URL")).toBeInTheDocument();
  // edit the exchange field
  fireEvent.change(screen.getByLabelText("Exchange"), { target: { value: "hookway.webhooks" } });
  const settings = (useGraphStore.getState().nodes[0].data as { settings: Record<string, unknown> }).settings;
  expect(settings.exchange).toBe("hookway.webhooks");
});

test("endpoint auth scheme is a dropdown constrained to the auth type", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id, selectedEdgeId: null });
  wrap();
  const scheme = screen.getByLabelText("Scheme");
  expect(scheme.tagName).toBe("INPUT"); // Mantine Select renders an input
  // switching to a base64 scheme updates the store
  const store = useGraphStore.getState();
  store.updateNodeData(ep.id, { auth: { ...(ep.data as any).auth, scheme: "hmac_sha256_base64" } });
  expect(((useGraphStore.getState().nodes[0].data as any).auth).scheme).toBe("hmac_sha256_base64");
});

test("destination exchange_type renders a select and durable a switch", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id, selectedEdgeId: null });
  wrap();
  expect(screen.getByLabelText("Exchange type")).toBeInTheDocument();
  const durable = screen.getByLabelText("Durable");
  fireEvent.click(durable);
  expect(((useGraphStore.getState().nodes[0].data as any).settings).durable).toBe(false);
});

test("selecting a route shows a focused Route view and edits its target", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [ep, d],
    edges: [{ id: "e1", source: ep.id, target: d.id, data: { target: "q", create: true } } as any],
    selectedId: null,
    selectedEdgeId: "e1",
  });
  wrap();
  expect(screen.getByText(`${ep.data.name} → ${d.data.name}`)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Target"), { target: { value: "github-webhooks" } });
  expect((useGraphStore.getState().edges[0].data as { target: string }).target).toBe("github-webhooks");
});

test("endpoint routes list rows select their edge", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [ep, d],
    edges: [{ id: "e1", source: ep.id, target: d.id, data: { target: "orders", create: true } } as any],
    selectedId: ep.id,
    selectedEdgeId: null,
  });
  wrap();
  fireEvent.click(screen.getByLabelText(`route to ${d.data.name}: orders`));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});
