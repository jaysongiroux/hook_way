import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Inspector } from "./Inspector";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters, emptyLookup } from "./model";
import type { VariableData } from "./model";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Inspector />
    </MantineProvider>,
  );
}

// section returns the accordion control button whose label starts with name.
// Controls carry a trailing summary badge, so the match is a prefix.
function section(name: string) {
  return screen.getByRole("button", { name: new RegExp(`^${name}`, "i") });
}

function variable(patch: Partial<VariableData> = {}): VariableData {
  return { name: "envName", source: "header", path: "", mode: "values", values: "", pattern: "", default: "", ...patch };
}

function selectEndpoint(data: Record<string, unknown> = {}) {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  Object.assign(ep.data, data);
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id, selectedEdgeId: null });
  return ep;
}

beforeEach(() => {
  resetIdCounters();
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: null });
});

test("an endpoint opens on Request with its other sections collapsed", () => {
  selectEndpoint();
  wrap();

  expect(section("Request")).toHaveAttribute("aria-expanded", "true");
  expect(section("Authentication")).toHaveAttribute("aria-expanded", "false");
  expect(section("Variables")).toHaveAttribute("aria-expanded", "false");
  expect(section("Routes")).toHaveAttribute("aria-expanded", "false");
  expect(section("Notes")).toHaveAttribute("aria-expanded", "false");
  // The open section's field is reachable; a collapsed section's is not.
  expect(screen.getByLabelText("Path")).toBeInTheDocument();
  expect(screen.queryByLabelText("Scheme")).not.toBeInTheDocument();
});

test("the name stays outside the sections so it is always editable", () => {
  const ep = selectEndpoint();
  wrap();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "digifi" } });
  expect((useGraphStore.getState().nodes[0].data as { name: string }).name).toBe("digifi");
  expect(ep.id).toBeTruthy();
});

// The bare Literal/Env var toggle used to sit unlabeled above the Variables
// box, so it read as part of Variables. It belongs to the auth secret.
test("the secret mode toggle is labeled as belonging to the auth secret", () => {
  selectEndpoint();
  wrap();
  fireEvent.click(section("Authentication"));

  expect(screen.getByText("Secret source")).toBeInTheDocument();
  const group = screen.getByRole("group", { name: "Secret source" });
  expect(group).toHaveTextContent("Literal");
  expect(group).toHaveTextContent("Env var");
});

test("the authentication header summarizes the configured auth type", () => {
  selectEndpoint();
  wrap();
  expect(section("Authentication")).toHaveTextContent("hmac_sha256");
});

test("expanding authentication reveals the scheme and secret fields", () => {
  selectEndpoint();
  wrap();
  fireEvent.click(section("Authentication"));

  expect(section("Authentication")).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByLabelText("Scheme")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Signature header"), { target: { value: "X-Hub-Signature-256" } });
  expect((useGraphStore.getState().nodes[0].data as { auth: { signatureHeader: string } }).auth.signatureHeader).toBe(
    "X-Hub-Signature-256",
  );
});

test("the variables header counts the declared variables", () => {
  selectEndpoint({ variables: [variable({ name: "envName" }), variable({ name: "tenant", source: "lookup", lookup: emptyLookup() })] });
  wrap();
  expect(section("Variables")).toHaveTextContent("2");
});

test("each variable is a collapsed row naming its source until expanded", () => {
  selectEndpoint({ variables: [variable({ name: "envName" }), variable({ name: "tenant", source: "query" })] });
  wrap();
  fireEvent.click(section("Variables"));

  const row = screen.getByRole("button", { name: /^envName/i });
  expect(row).toHaveTextContent("header");
  expect(screen.getByRole("button", { name: /^tenant/i })).toHaveTextContent("query");
  // Collapsed rows keep their fields out of the sidebar entirely.
  expect(screen.queryByLabelText("Variable name")).not.toBeInTheDocument();

  fireEvent.click(row);
  expect(screen.getByLabelText("Variable name")).toHaveValue("envName");
});

test("adding a variable expands it so it can be filled in right away", () => {
  selectEndpoint();
  wrap();
  fireEvent.click(section("Variables"));
  fireEvent.click(screen.getByText("+ Add variable"));

  fireEvent.change(screen.getByLabelText("Variable name"), { target: { value: "envName" } });
  const vars = (useGraphStore.getState().nodes[0].data as { variables?: VariableData[] }).variables;
  expect(vars).toHaveLength(1);
  expect(vars![0].name).toBe("envName");
});

test("a destination opens on Settings and lists its incoming routes in a section", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [ep, d],
    edges: [{ id: "e1", source: ep.id, target: d.id, data: { target: "orders", create: true } } as never],
    selectedId: d.id,
    selectedEdgeId: null,
  });
  wrap();

  expect(section("Settings")).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByLabelText("AMQP URL")).toBeInTheDocument();
  expect(section("Incoming routes")).toHaveTextContent("1");

  fireEvent.click(section("Incoming routes"));
  fireEvent.click(screen.getByLabelText(`route from ${ep.data.name}: orders`));
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});

test("a filter opens on its condition", () => {
  const f = createNode("filter", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [f], edges: [], selectedId: f.id, selectedEdgeId: null });
  wrap();

  expect(section("Condition")).toHaveAttribute("aria-expanded", "true");
  fireEvent.change(screen.getByLabelText("Path"), { target: { value: "action" } });
  expect((useGraphStore.getState().nodes[0].data as { path: string }).path).toBe("action");
});
