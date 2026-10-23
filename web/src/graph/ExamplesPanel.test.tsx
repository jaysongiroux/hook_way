import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ExamplesPanel, ExampleImportDialog } from "./ExamplesPanel";
import { createExampleCatalog } from "./exampleCatalog";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";

const loading = vi.hoisted(() => ({ fail: false }));
vi.mock("./repositoryExamples", async () => {
  const { createExampleCatalog } = await import("./exampleCatalog");
  return {
    get repositoryExamples() {
      if (loading.fail) throw new Error("Catalogue unavailable");
      return createExampleCatalog({ "nested/notification.yaml": "flows: {orders: {nodes: [{id: in, type: http_trigger}], edges: []}}" });
    },
  };
});

const [example] = createExampleCatalog({
  "flows-demo.yaml": `server: {addr: ':9090'}
credentials: {vendor: {kind: static}}
flows:
  orders:
    nodes: [{id: in, type: http_trigger, path: /orders}]
    edges: []
  billing:
    nodes: [{id: bill, type: http_trigger, path: /billing}, {id: ack, type: respond, status: 200}]
    edges: [bill>ack]
`,
});

beforeEach(() => { useGraphStore.getState().clear(); loading.fail = false; vi.restoreAllMocks(); });
function wrap(component: React.ReactNode) { return render(<MantineProvider theme={theme}>{component}</MantineProvider>); }

test("loads the catalogue, searches by path and flow, and presents an accessible empty state", async () => {
  wrap(<ExamplesPanel />);
  expect(screen.getByRole("status")).toHaveTextContent("Loading examples");
  expect(await screen.findByRole("button", { name: "Import example nested/notification.yaml" })).toBeEnabled();
  const search = screen.getByRole("textbox", { name: "Search examples" });
  fireEvent.change(search, { target: { value: "orders" } });
  expect(screen.getByText("Notification")).toBeInTheDocument();
  fireEvent.change(search, { target: { value: "nested/" } });
  expect(screen.getByText("Notification")).toBeInTheDocument();
  fireEvent.change(search, { target: { value: "missing" } });
  expect(screen.getByRole("status")).toHaveTextContent("No examples match");
});

test("failed catalogue loading supports retry", async () => {
  loading.fail = true;
  wrap(<ExamplesPanel />);
  expect(await screen.findByRole("alert")).toHaveTextContent("Catalogue unavailable");
  loading.fail = false;
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("button", { name: "Import example nested/notification.yaml" })).toBeEnabled();
});

test("nodes mode defaults to the first flow, updates counts, and appends only the selected flow", () => {
  const onImported = vi.fn(), onClose = vi.fn();
  wrap(<ExampleImportDialog example={example} onClose={onClose} onImported={onImported} />);
  expect(screen.getByRole("radio", { name: "Nodes only" })).toBeChecked();
  expect(screen.getByText(/References are preserved/)).toBeInTheDocument();
  expect(screen.getByText("1 node · 0 edges")).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Source flow"), { target: { value: "billing" } });
  expect(screen.getByText("2 nodes · 1 edge")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Import nodes" }));
  expect(useGraphStore.getState().nodes.map((node) => node.id)).toEqual(["bill", "ack"]);
  expect(useGraphStore.getState().configExtra).toEqual({});
  expect(onImported).toHaveBeenCalledOnce();
  expect(onClose).toHaveBeenCalledOnce();
});

test("core replacement confirms its effect and preserves the independent replay document", () => {
  const state = useGraphStore.getState();
  state.setGraph(fromYaml("flows: {old: {nodes: [{id: old, type: respond}], edges: []}}\n"));
  state.setReplayServiceConfig({ database: { url: "existing" } });
  state.setReplayFilename("private-replay.yml");
  wrap(<ExampleImportDialog example={example} onClose={vi.fn()} />);
  fireEvent.click(screen.getByRole("radio", { name: "Nodes + core config" }));
  expect(screen.getByText(/All current core flows and settings will be replaced/)).toBeInTheDocument();
  expect(screen.queryByLabelText("Source flow")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Replace core configuration" }));
  expect(useGraphStore.getState().activeFlow).toBe("orders");
  expect(Object.keys(useGraphStore.getState().otherFlows)).toEqual(["billing"]);
  expect(useGraphStore.getState().serverConfig.addr).toBe(":9090");
  expect(useGraphStore.getState().replayServiceConfig).toEqual({ database: { url: "existing" } });
  expect(useGraphStore.getState().replayFilename).toBe("private-replay.yml");
});

test("cancel and malformed bundles leave the workspace intact", () => {
  const before = useGraphStore.getState().document(), close = vi.fn();
  const { unmount } = wrap(<ExampleImportDialog example={example} onClose={close} />);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(close).toHaveBeenCalledOnce();
  expect(useGraphStore.getState().document()).toEqual(before);
  unmount();
  wrap(<ExampleImportDialog example={{ ...example, files: {} }} onClose={vi.fn()} />);
  expect(screen.getByRole("alert")).toHaveTextContent("Missing entry YAML");
  expect(screen.getByRole("button", { name: "Import nodes" })).toBeDisabled();
  expect(useGraphStore.getState().document()).toEqual(before);
});

test("an import failure stays in the dialog and does not report success", async () => {
  const onImported = vi.fn();
  vi.spyOn(useGraphStore.getState(), "appendExampleGraph").mockImplementation(() => { throw new Error("Import interrupted"); });
  wrap(<ExampleImportDialog example={example} onClose={vi.fn()} onImported={onImported} />);
  fireEvent.click(screen.getByRole("button", { name: "Import nodes" }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Import interrupted"));
  expect(onImported).not.toHaveBeenCalled();
});
