import { expect, test, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { usePersistence } from "./usePersistence";
import { useGraphStore } from "./store";
import { saveGraph, clearGraph } from "./persistence";
import { createNode, emptyDocument, resetIdCounters } from "./model";

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
