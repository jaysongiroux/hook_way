import { expect, test, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { usePersistence } from "./usePersistence";
import { useGraphStore } from "./store";
import { saveGraph, clearGraph } from "./persistence";
import { createNode, resetIdCounters } from "./model";

function Harness() {
  usePersistence();
  return null;
}

beforeEach(async () => {
  resetIdCounters();
  await clearGraph();
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: null });
});

test("rehydrates the store from a saved graph on mount", async () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  await saveGraph([ep], []);
  render(<Harness />);
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(1));
});
