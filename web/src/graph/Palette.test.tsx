import { expect, test, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Palette, DND_MIME, DND_TEMPLATE_MIME } from "./Palette";
import { NODE_TYPES, nodeLabel } from "./nodeSchema";
import { TEMPLATES } from "./templates";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <Palette />
    </MantineProvider>,
  );
}

test("renders a palette entry with a short description for every catalog node type", () => {
  wrap();
  for (const type of NODE_TYPES) {
    expect(screen.getByText(nodeLabel(type))).toBeInTheDocument();
  }
  expect(screen.getByText(/decodes the verified body/i)).toBeInTheDocument();
});

test("dragging an item sets the node type on the dataTransfer", () => {
  wrap();
  const setData = vi.fn();
  const item = screen.getByText("http trigger").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_MIME, "http_trigger");
});

test("renders a palette entry for every template", () => {
  wrap();
  for (const def of TEMPLATES) {
    expect(screen.getByText(def.label)).toBeInTheDocument();
    expect(screen.getByText(def.help)).toBeInTheDocument();
  }
});

test("dragging a template sets the template id on its own mime type", () => {
  wrap();
  const setData = vi.fn();
  const item = screen.getByText("Slack notification").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_TEMPLATE_MIME, "slack-notify");
  expect(setData).not.toHaveBeenCalledWith(DND_MIME, expect.anything());
});
