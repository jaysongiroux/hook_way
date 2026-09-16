import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "./theme";
import App from "./App";
import { useGraphStore } from "./graph/store";
import { createNode, resetIdCounters } from "./graph/model";

import { clearGraph } from "./graph/persistence";

function renderApp() {
  return render(
    <MantineProvider theme={theme}>
      <App />
    </MantineProvider>,
  );
}

test("renders the shell header and palette node items", async () => {
  renderApp();
  expect(screen.getByText("Hookway Config")).toBeInTheDocument();
  expect(screen.getByText("http trigger")).toBeInTheDocument();
  expect(screen.getByText("filter")).toBeInTheDocument();
  expect(screen.getByText("publish")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Inspector" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Active flow" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Config" }));
  expect(await screen.findByRole("menuitem", { name: "Import YAML" })).toBeInTheDocument();
  expect(screen.getByRole("menuitem", { name: "Server" })).toBeInTheDocument();
});

test("Clear asks for confirmation before wiping the graph", async () => {
  resetIdCounters();
  await clearGraph();
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [] });
  renderApp();
  fireEvent.click(screen.getByRole("button", { name: "Clear" }));
  expect(await screen.findByRole("heading", { name: /clear the canvas/i })).toBeInTheDocument();
  expect(useGraphStore.getState().nodes).toHaveLength(1);
  fireEvent.click(screen.getByRole("button", { name: "Clear canvas" }));
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(0));
});
