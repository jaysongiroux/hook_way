import { expect, test, beforeEach, afterEach, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";
import { usePersistence } from "./usePersistence";
import { useGraphStore } from "./store";
import { saveGraph, clearGraph } from "./persistence";
import * as persistence from "./persistence";
import { createNode, emptyDocument, resetIdCounters, type GraphDocument } from "./model";
import { starterDocument } from "./starter";

function Harness() {
  usePersistence();
  return null;
}

beforeEach(async () => {
  resetIdCounters();
  await clearGraph();
  useGraphStore.getState().clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

test("rehydrates the store from a saved graph on mount", async () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  await saveGraph({ ...emptyDocument(), nodes: [n], edges: [] });
  render(<Harness />);
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(1));
});

test("loads the production starter when nothing is saved", async () => {
  const starter = starterDocument();
  render(<Harness />);
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(starter.nodes.length));
  expect(useGraphStore.getState().activeFlow).toBe("github");
});

test("rehydrates a replay-only draft after core was cleared", async () => {
  await saveGraph({ ...emptyDocument(), replayServiceConfig: { server: { addr: ":8080" } } });
  render(<Harness />);
  await waitFor(() => expect(useGraphStore.getState().replayServiceConfig).toEqual({ server: { addr: ":8080" } }));
  expect(useGraphStore.getState().nodes).toHaveLength(0);
});

test("rehydrates a core draft with server settings but no graph nodes", async () => {
  await saveGraph({ ...emptyDocument(), serverConfig: { addr: ":9090" }, nodes: [], edges: [] });
  render(<Harness />);
  await waitFor(() => expect(useGraphStore.getState().serverConfig).toEqual({ addr: ":9090" }));
  expect(useGraphStore.getState().nodes).toHaveLength(0);
});

test("keeps an import made while a saved draft is loading and saves only the import", async () => {
  const savedNode = createNode("http_trigger", { x: 0, y: 0 });
  const importedNode = createNode("http_trigger", { x: 100, y: 100 });
  let resolveLoad!: (graph: GraphDocument | null) => void;
  const loading = new Promise<GraphDocument | null>((resolve) => { resolveLoad = resolve; });
  vi.spyOn(persistence, "loadGraph").mockReturnValue(loading);
  const save = vi.spyOn(persistence, "saveGraph").mockResolvedValue();

  render(<Harness />);
  act(() => useGraphStore.getState().setGraph({ ...emptyDocument(), nodes: [importedNode] }));
  expect(useGraphStore.getState().nodes).toEqual([importedNode]);
  expect(save).not.toHaveBeenCalled();

  await act(async () => resolveLoad({ ...emptyDocument(), nodes: [savedNode] }));
  expect(useGraphStore.getState().nodes).toEqual([importedNode]);
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ nodes: [importedNode] }));
});

test("selection while loading does not prevent saved draft hydration", async () => {
  const savedNode = createNode("http_trigger", { x: 0, y: 0 });
  let resolveLoad!: (graph: GraphDocument | null) => void;
  vi.spyOn(persistence, "loadGraph").mockReturnValue(new Promise((resolve) => { resolveLoad = resolve; }));

  render(<Harness />);
  act(() => useGraphStore.getState().setSelected("temporary-selection"));
  await act(async () => resolveLoad({ ...emptyDocument(), nodes: [savedNode] }));
  expect(useGraphStore.getState().nodes).toEqual([savedNode]);
});

test("an edit made before an empty draft loads is kept instead of the starter", async () => {
  let resolveLoad!: (graph: GraphDocument | null) => void;
  vi.spyOn(persistence, "loadGraph").mockReturnValue(new Promise((resolve) => { resolveLoad = resolve; }));

  render(<Harness />);
  act(() => useGraphStore.getState().setServerConfig({ addr: ":9090" }));
  await act(async () => resolveLoad(null));
  expect(useGraphStore.getState().serverConfig).toMatchObject({ addr: ":9090" });
  expect(useGraphStore.getState().nodes).toHaveLength(0);
});
