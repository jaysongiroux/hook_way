import { expect, test, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Palette, DND_MIME } from "./Palette";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Palette />
    </MantineProvider>,
  );
}

test("renders draggable Endpoint and Destination items", () => {
  wrap();
  expect(screen.getByText("Endpoint")).toBeInTheDocument();
  expect(screen.getByText("Destination")).toBeInTheDocument();
});

test("dragging an item sets the node kind on the dataTransfer", () => {
  wrap();
  const setData = vi.fn();
  const item = screen.getByText("Endpoint").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_MIME, "endpoint");
});

test("renders a draggable Filter item that sets the filter kind", () => {
  wrap();
  expect(screen.getByText("Filter")).toBeInTheDocument();
  const setData = vi.fn();
  const item = screen.getByText("Filter").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_MIME, "filter");
});
