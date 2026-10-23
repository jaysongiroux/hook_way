import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { EventsPanel } from "./EventsPanel";
import { useGraphStore } from "./store";
import { createNode } from "./model";
import { fromYaml } from "./fromYaml";
import { fromBundle, toBundle } from "./bundle";
import { clearGraph, loadGraph, saveGraph } from "./persistence";
import { SimpleOptionsForm } from "./SimpleOptionsForm";
import { WorkspaceActions } from "./FormPresentation";

beforeEach(async () => {
  useGraphStore.getState().clear();
  await clearGraph();
});

const entries = () => useGraphStore.getState().configExtra.events as Record<string, Record<string, unknown>>;
const initial = { description: "Opened issue", payload_schema: { type: "object" } };

test("edits event definitions, prevents collisions and retains references when renaming", () => {
  const node = createNode("event", { x: 0, y: 0 });
  node.data.options = { event: "issue.opened" };
  useGraphStore.setState({ nodes: [node], configExtra: { events: { "issue.opened": initial }, custom: 42 } });
  render(<MantineProvider><EventsPanel opened onClose={() => {}} /></MantineProvider>);
  fireEvent.change(screen.getByLabelText("Description"), { target: { value: "New issue" } });
  expect(entries()["issue.opened"].description).toBe("New issue");
  fireEvent.change(screen.getByLabelText("New event name"), { target: { value: "issue.opened" } });
  fireEvent.click(screen.getByRole("button", { name: "Add event" }));
  expect(screen.getByText("That name already exists.")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "issue.created" } });
  fireEvent.click(screen.getByRole("button", { name: "Rename" }));
  expect(entries()["issue.created"].description).toBe("New issue");
  expect(entries()["issue.opened"]).toBeUndefined();
  expect(useGraphStore.getState().nodes[0].data.options.event).toBe("issue.opened");
  expect(useGraphStore.getState().configExtra.custom).toBe(42);
  fireEvent.click(screen.getByRole("button", { name: "Remove event issue.created" }));
  expect(entries()).toEqual({});
});

test("invalid schema drafts survive save/reload and renames without changing exported schemas", async () => {
  useGraphStore.getState().setConfigExtra({ events: { "issue.opened": initial } });
  const panel = render(<MantineProvider><EventsPanel opened onClose={() => {}} /></MantineProvider>);
  fireEvent.change(screen.getByLabelText("Payload schema"), { target: { value: '{"type":"object","minimum":9007199254740993}' } });
  expect(screen.getByText(/would lose precision/)).toBeInTheDocument();
  expect(entries()["issue.opened"].payload_schema).toEqual({ type: "object" });
  await saveGraph(useGraphStore.getState().document());
  const loaded = await loadGraph();
  expect(Object.values(loaded?.formDrafts ?? {})[0].text).toContain("9007199254740993");
  panel.unmount();
  useGraphStore.getState().setGraph(loaded!);
  render(<MantineProvider><EventsPanel opened onClose={() => {}} /></MantineProvider>);
  expect(screen.getByLabelText("Payload schema")).toHaveValue('{"type":"object","minimum":9007199254740993}');
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "issue.created" } });
  fireEvent.click(screen.getByRole("button", { name: "Rename" }));
  expect(Object.keys(useGraphStore.getState().formDrafts ?? {})).toEqual(["events/issue.created|events.issue.created.payload_schema"]);
  fireEvent.click(screen.getByRole("button", { name: "Revert payload schema draft" }));
  expect(screen.getByLabelText("Payload schema")).toHaveValue(JSON.stringify({ type: "object" }, null, 2));
});

test("event selector keeps missing references repairable and opens catalog management", () => {
  useGraphStore.getState().setConfigExtra({ events: { "issue.opened": initial } });
  const manage = vi.fn();
  render(<MantineProvider><WorkspaceActions.Provider value={manage}>
    <SimpleOptionsForm nodeType="event" scope="node/test/event" value={{ event: "missing" }} onChange={() => {}} />
  </WorkspaceActions.Provider></MantineProvider>);
  expect(screen.getAllByText(/missing/).length).toBeGreaterThan(0);
  expect(screen.getByLabelText("Event ID path")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Manage events" }));
  expect(manage).toHaveBeenCalledWith("events");
});

test("event catalogs and schema literals round-trip single/split workspaces and flow switches", async () => {
  const text = `server: {addr: ':8080', max_body_bytes: 1048576}
events:
  issue.opened:
    description: Issue
    payload_schema:
      type: object
      properties:
        title: {type: string, const: '\u0024{LITERAL}', default: '\u0024literal'}
        id: {type: integer, minimum: 9007199254740992}
flows:
  first:
    nodes: [{id: e, type: event, event: issue.opened}]
    edges: []
  second:
    nodes: [{id: second, type: mcp_result, source: body}]
    edges: []
`;
  useGraphStore.getState().setGraph(fromYaml(text));
  useGraphStore.getState().setActiveFlow("second");
  useGraphStore.getState().setActiveFlow("first");
  useGraphStore.getState().setYAMLMode("split");
  const doc = useGraphStore.getState().document();
  const bundle = toBundle(doc);
  expect(bundle.files[bundle.entry]).toContain("events:");
  expect(bundle.files[bundle.entry]).toContain("\\u0024{LITERAL}");
  expect(bundle.files["flows/first.yaml"]).not.toContain("events:");
  const restored = fromBundle(bundle);
  expect(restored.configExtra.events).toEqual(doc.configExtra.events);
  expect(restored.nodes[0].data.options.event).toBe("issue.opened");
  await saveGraph(restored);
  expect((await loadGraph())?.configExtra.events).toEqual(doc.configExtra.events);
  expect(() => fromYaml(text.replace("9007199254740992", "9007199254740993"))).toThrow(/precision/);
  expect(() => fromBundle({ entry: "core.yaml", files: { "core.yaml": "flows: {first: {file: flow.yaml}}", "flow.yaml": "events: {}\nnodes: []\nedges: []" } })).toThrow(/belongs in the entry/);
});
