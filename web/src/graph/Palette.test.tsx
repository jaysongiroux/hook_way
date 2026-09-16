import { expect, test, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Palette, DND_MIME } from "./Palette";
import { NODE_TYPES, nodeLabel } from "./nodeSchema";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Palette />
    </MantineProvider>,
  );
}

test("renders a palette entry for every catalog node type", () => {
  wrap();
  for (const type of NODE_TYPES) {
    expect(screen.getByText(nodeLabel(type))).toBeInTheDocument();
  }
});

test("dragging an item sets the node type on the dataTransfer", () => {
  wrap();
  const setData = vi.fn();
  const item = screen.getByText("http trigger").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_MIME, "http_trigger");
});
