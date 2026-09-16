import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "./theme";
import App from "./App";
import { useGraphStore } from "./graph/store";
import { createNode, emptyDocument, resetIdCounters } from "./graph/model";
import { saveGraph, clearGraph } from "./graph/persistence";

function renderApp() {
  return render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <App />
      </MemoryRouter>
    </MantineProvider>,
  );
}

test("renders the shell header and palette node items", async () => {
  renderApp();
  expect(screen.getByRole("link", { name: "Hookway home" })).toHaveAttribute("href", "/");
  expect(screen.getAllByText("http trigger").length).toBeGreaterThan(0);
  expect(screen.getAllByText("filter").length).toBeGreaterThan(0);
  expect(screen.getAllByText("publish").length).toBeGreaterThan(0);
  expect(screen.getByRole("button", { name: "Inspector" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Active flow" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Config" }));
  expect(await screen.findByRole("menuitem", { name: "Import YAML" })).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: "Load production example" })).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: "Server" })).toBeInTheDocument();
});

test("Clear asks for confirmation before wiping the graph", async () => {
  resetIdCounters();
  await clearGraph();
  const n = createNode("http_trigger", { x: 0, y: 0 });
  const doc = { ...emptyDocument(), nodes: [n], edges: [] };
  await saveGraph(doc);
  useGraphStore.setState({ ...doc, selectedId: null, selectedEdgeId: null });
  renderApp();
  fireEvent.click(screen.getByRole("button", { name: "Clear" }));
  expect(await screen.findByRole("heading", { name: /clear the canvas/i })).toBeInTheDocument();
  expect(useGraphStore.getState().nodes).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Clear canvas" }));
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(0));
});
