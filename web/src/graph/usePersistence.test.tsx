import { expect, test, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { usePersistence } from "./usePersistence";
import { useGraphStore } from "./store";
import { saveGraph, clearGraph } from "./persistence";
import { createNode, emptyDocument, resetIdCounters } from "./model";
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
