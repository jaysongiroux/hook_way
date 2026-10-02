import { expect, test, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, act, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { InspectorModal } from "./InspectorModal";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

function wrap(opened: boolean, onClose = vi.fn()) {
  return render(
    <MantineProvider theme={theme}>
      <InspectorModal opened={opened} onClose={onClose} />
    </MantineProvider>,
  );
}

beforeEach(() => {
  resetIdCounters();
  useGraphStore.getState().clear();
});

test("stays out of the DOM until it is opened", () => {
  wrap(false);
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

test("keeps the accessible name 'Inspector' while showing the selected node", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [n], edges: [], selectedId: n.id });
  wrap(true);

  const dialog = screen.getByRole("dialog", { name: "Inspector" });
  expect(dialog).toBeInTheDocument();
  expect(
    within(dialog).getByRole("button", { name: "Identity and description" }),
  ).toHaveAttribute("aria-expanded", "false");
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Identity and description" }),
  );
  expect(screen.getByLabelText("Node ID")).toHaveValue("http_trigger_1");
});

test("reports a close when the close button is pressed", () => {
  const onClose = vi.fn();
  wrap(true, onClose);

  fireEvent.click(screen.getByRole("button", { name: /close/i }));

  expect(onClose).toHaveBeenCalled();
});

test("caps the scrollport so a form taller than the dialog can be reached", () => {
  wrap(true);
  const viewport = document.querySelector(
    ".mantine-ScrollArea-viewport",
  ) as HTMLElement;
  expect(
    viewport,
    "the inspector modal should own a scroll viewport",
  ).not.toBeNull();
  // max-height on the root clips; it has to land on the viewport to scroll.
  expect(viewport.style.maxHeight).toBe("70vh");
});

test("scrolls back to the top when the selection changes", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 200, y: 0 });
  useGraphStore.setState({ nodes: [a, b], edges: [], selectedId: a.id });
  const { container } = wrap(true);
  const viewport = container.ownerDocument.querySelector(
    ".mantine-ScrollArea-viewport",
  ) as HTMLElement;
  expect(
    viewport,
    "the inspector modal should own a scroll viewport",
  ).not.toBeNull();
  viewport.scrollTop = 180;

  act(() => useGraphStore.getState().setSelected(b.id));

  expect(viewport.scrollTop).toBe(0);
});
