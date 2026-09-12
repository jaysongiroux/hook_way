import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Inspector } from "./Inspector";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";
import { DEST_TYPE_SPECS } from "./destTypes";

// Throwaway spec used only to exercise the generic "group" field mechanism;
// no real destination type has a group field yet.
DEST_TYPE_SPECS.__group_test__ = {
  block: "__group_test__",
  fields: [
    { key: "url", label: "URL" },
    {
      key: "retry",
      label: "Retry",
      kind: "group",
      fields: [{ key: "max_attempts", label: "Max attempts", kind: "number" }],
    },
  ],
};

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Inspector />
    </MantineProvider>,
  );
}

// The inspector groups fields into collapsible sections; only the first one is
// open on selection, so most fields need their section opened first.
function openSection(name: string) {
  fireEvent.click(screen.getByRole("button", { name: new RegExp(`^${name}`, "i") }));
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

test("renaming a destination updates the store", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id });
  wrap();
  const nameInput = screen.getByLabelText("Name") as HTMLInputElement;
  fireEvent.change(nameInput, { target: { value: "primary" } });
  expect((useGraphStore.getState().nodes[0].data as { name: string }).name).toBe("primary");
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
  openSection("Notes");
  fireEvent.change(screen.getByRole("textbox", { name: "Notes" }), { target: { value: "audit queue" } });
  expect((useGraphStore.getState().nodes[0].data as { notes?: string }).notes).toBe("audit queue");
});

test("changing destination type reseeds settings and renders its fields", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id });
  wrap();
  // rabbitmq default → AMQP URL field present
  expect(screen.getByLabelText("AMQP URL")).toBeInTheDocument();
  // edit the top-level exchange field (dead_letter group also has an "Exchange")
  fireEvent.change(screen.getAllByLabelText("Exchange")[0], { target: { value: "hookway.webhooks" } });
  const settings = (useGraphStore.getState().nodes[0].data as { settings: Record<string, unknown> }).settings;
  expect(settings.exchange).toBe("hookway.webhooks");
});

test("endpoint auth scheme is a dropdown constrained to the auth type", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id, selectedEdgeId: null });
  wrap();
  openSection("Authentication");
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
  // top-level controls come first; dead_letter group reuses these labels.
  expect(screen.getAllByLabelText("Exchange type")[0]).toBeInTheDocument();
  const durable = screen.getAllByLabelText("Durable")[0];
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
  openSection("Routes");
  fireEvent.click(screen.getByLabelText(`route to ${d.data.name}: orders`));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});

test("destination group field renders a Fieldset legend and editing a child updates the nested settings object", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  (d.data as any).destType = "__group_test__";
  (d.data as any).settings = { url: "", retry: { max_attempts: "" } };
  useGraphStore.setState({ nodes: [d], edges: [], selectedId: d.id });
  wrap();
  expect(screen.getByText("Retry")).toBeInTheDocument();
  const maxAttempts = screen.getByLabelText("Max attempts") as HTMLInputElement;
  fireEvent.change(maxAttempts, { target: { value: "5" } });
  const settings = (useGraphStore.getState().nodes[0].data as { settings: Record<string, unknown> }).settings;
  expect(settings.retry).toEqual({ max_attempts: 5 });
});

test("destination shows incoming routes and a row selects its edge", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [ep, d],
    edges: [{ id: "e1", source: ep.id, target: d.id, data: { target: "orders", create: true } } as any],
    selectedId: d.id,
    selectedEdgeId: null,
  });
  wrap();
  expect(screen.getByRole("button", { name: /^Incoming routes/i })).toHaveTextContent("1");
  openSection("Incoming routes");
  fireEvent.click(screen.getByLabelText(`route from ${ep.data.name}: orders`));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});

test("adding a variable to an endpoint updates the store", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id });
  wrap();
  openSection("Variables");
  fireEvent.click(screen.getByText("+ Add variable"));
  fireEvent.change(screen.getByLabelText("Variable name"), { target: { value: "envName" } });
  fireEvent.change(screen.getByLabelText("Values (comma-separated)"), { target: { value: "qa-gm3, portal" } });
  const data = useGraphStore.getState().nodes[0].data as { variables?: { name: string; values: string }[] };
  expect(data.variables).toHaveLength(1);
  expect(data.variables![0].name).toBe("envName");
  expect(data.variables![0].values).toBe("qa-gm3, portal");
  expect(screen.getByLabelText("Fallback destination")).toBeInTheDocument();
});

test("a lookup variable shows provider fields and edits them", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  (ep.data as any).variables = [{
    name: "env", source: "lookup", path: "", mode: "any", values: "", pattern: "", default: "",
    lookup: { keySource: "body", keyPath: "", provider: "http", dynamodb: { table: "", index: "", value_attribute: "", filter_attribute: "", filter_value: "" }, http: { url: "", method: "GET", value_path: "" }, postgres: { url: "", query: "" } },
  }];
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id });
  wrap();
  openSection("Variables");
  fireEvent.click(screen.getByRole("button", { name: /^env/i }));
  expect(screen.getByLabelText("Provider")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("URL (with {key})"), { target: { value: "https://r/{key}" } });
  expect((useGraphStore.getState().nodes[0].data as any).variables[0].lookup.http.url).toBe("https://r/{key}");
});
