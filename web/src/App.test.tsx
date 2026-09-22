import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
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

test("the inspector stays closed until something opens it", async () => {
  resetIdCounters();
  await clearGraph();
  useGraphStore.setState({ ...emptyDocument(), nodes: [], edges: [], selectedId: null });
  renderApp();

  expect(screen.queryByRole("dialog", { name: "Inspector" })).not.toBeInTheDocument();
});

test("the header Inspector button opens the inspector modal", async () => {
  resetIdCounters();
  await clearGraph();
  useGraphStore.setState({ ...emptyDocument(), nodes: [], edges: [], selectedId: null });
  renderApp();

  fireEvent.click(screen.getByRole("button", { name: "Inspector" }));

  expect(await screen.findByRole("dialog", { name: "Inspector" })).toBeInTheDocument();
});

test("double clicking a node on the canvas opens the inspector on that node", async () => {
  resetIdCounters();
  await clearGraph();
  const n = createNode("http_trigger", { x: 0, y: 0 });
  // Seeded through persistence rather than the store: usePersistence loads
  // asynchronously on mount and would otherwise replace a directly-set graph
  // with the starter document mid-test.
  await saveGraph({ ...emptyDocument(), nodes: [n], edges: [] });
  const { container } = renderApp();
  await waitFor(() => expect(container.querySelectorAll(".react-flow__node")).toHaveLength(1));

  fireEvent.click(container.querySelector(".react-flow__node")!);
  // The modal fades in over Mantine's default transition, so this has to
  // outwait that transition -- checked any sooner, the absence assertion
  // passes no matter what a single click does.
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 400));
  });
  expect(screen.queryByRole("dialog", { name: "Inspector" }), "a single click should not open the inspector").toBeNull();
  expect(useGraphStore.getState().selectedId, "a single click should still select").toBe("http_trigger_1");

  fireEvent.doubleClick(container.querySelector(".react-flow__node")!);

  const dialog = await screen.findByRole("dialog", { name: "Inspector" });
  expect(within(dialog).getByLabelText("Id")).toHaveValue("http_trigger_1");
});

test("the builder keeps its draggable node palette docked on the left", () => {
  renderApp();

  const item = screen.getAllByText("http trigger")[0].closest("[draggable=true]");
  expect(item, "palette items must stay drag sources for the canvas").not.toBeNull();
  expect(screen.getByRole("separator", { name: "Resize Nodes" })).toBeInTheDocument();
});

test("Config offers flow settings, which opens the inspector on the whole flow", async () => {
  resetIdCounters();
  await clearGraph();
  const n = createNode("publish", { x: 0, y: 0 });
  await saveGraph({ ...emptyDocument(), nodes: [n], edges: [] });
  renderApp();
  await waitFor(() => expect(useGraphStore.getState().nodes).toHaveLength(1));
  useGraphStore.getState().setSelected(n.id);

  fireEvent.click(screen.getByRole("button", { name: "Config" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "Flow settings" }));

  const dialog = await screen.findByRole("dialog", { name: "Inspector" });
  expect(useGraphStore.getState().selectedId).toBeNull();
  expect(within(dialog).getByLabelText("Flow name")).toBeInTheDocument();
});

test("the Config button shows that it opens a dropdown", () => {
  renderApp();

  const config = screen.getByRole("button", { name: "Config" });
  expect(config).toHaveAttribute("aria-haspopup", "menu");
  expect(config.querySelector("svg"), "Config needs a visible caret, not just the aria hint").not.toBeNull();
});
