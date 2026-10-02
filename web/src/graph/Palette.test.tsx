import { expect, test, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import {
  Palette,
  PaletteTabs,
  DND_MIME,
  DND_TEMPLATE_MIME,
  type PaletteTab,
} from "./Palette";
import { NODE_TYPES, nodeLabel } from "./nodeSchema";
import { TEMPLATES } from "./templates";

function wrap(tab: PaletteTab = "nodes") {
  return render(
    <MantineProvider theme={theme}>
      <Palette tab={tab} />
    </MantineProvider>,
  );
}

function wrapTabs(value: PaletteTab, onChange: (t: PaletteTab) => void) {
  return render(
    <MantineProvider theme={theme}>
      <PaletteTabs value={value} onChange={onChange} />
    </MantineProvider>,
  );
}

test("renders a palette entry with a short description for every catalog node type", () => {
  wrap("nodes");
  for (const type of NODE_TYPES) {
    expect(screen.getByText(nodeLabel(type))).toBeInTheDocument();
  }
  expect(screen.getByText(/Decode the verified body/i)).toBeInTheDocument();
});

test("dragging an item sets the node type on the dataTransfer", () => {
  wrap("nodes");
  const setData = vi.fn();
  const item = screen.getByText("HTTP endpoint").closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_MIME, "http_trigger");
});

test("renders a palette entry for every template", () => {
  wrap("templates");
  for (const def of TEMPLATES) {
    expect(screen.getByText(def.label)).toBeInTheDocument();
    expect(screen.getByText(def.help)).toBeInTheDocument();
  }
});

test("dragging a template sets the template id on its own mime type", () => {
  wrap("templates");
  const setData = vi.fn();
  const item = screen
    .getByText("Slack notification")
    .closest("[draggable=true]");
  expect(item).not.toBeNull();
  fireEvent.dragStart(item!, { dataTransfer: { setData, effectAllowed: "" } });
  expect(setData).toHaveBeenCalledWith(DND_TEMPLATE_MIME, "slack-notify");
  expect(setData).not.toHaveBeenCalledWith(DND_MIME, expect.anything());
});

test("node entries expose an accessible Add action without removing drag support", () => {
  const onAddNode = vi.fn();
  render(
    <MantineProvider theme={theme}>
      <Palette tab="nodes" onAddNode={onAddNode} />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add HTTP endpoint" }));
  expect(onAddNode).toHaveBeenCalledWith("http_trigger");
  expect(
    screen.getByText("HTTP endpoint").closest("[draggable=true]"),
  ).not.toBeNull();
});

test("template entries expose an accessible Add action", () => {
  const onAddTemplate = vi.fn();
  render(
    <MantineProvider theme={theme}>
      <Palette tab="templates" onAddTemplate={onAddTemplate} />
    </MantineProvider>,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Add Slack notification" }),
  );
  expect(onAddTemplate).toHaveBeenCalledWith("slack-notify");
});

test("the nodes tab shows no template entries", () => {
  wrap("nodes");
  for (const def of TEMPLATES) {
    expect(screen.queryByText(def.label), def.id).toBeNull();
  }
});

test("the templates tab shows no node entries", () => {
  wrap("templates");
  for (const type of NODE_TYPES) {
    expect(screen.queryByText(nodeLabel(type)), type).toBeNull();
  }
});

test("the tab strip marks the active tab and leaves the other selectable", () => {
  wrapTabs("nodes", vi.fn());
  expect(screen.getByRole("tab", { name: "Nodes" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  expect(screen.getByRole("tab", { name: "Templates" })).toHaveAttribute(
    "aria-selected",
    "false",
  );
});

test("clicking the inactive tab reports the new tab to the caller", () => {
  const onChange = vi.fn();
  wrapTabs("nodes", onChange);
  fireEvent.click(screen.getByRole("tab", { name: "Templates" }));
  expect(onChange).toHaveBeenCalledWith("templates");
});

test("clicking the already-active tab does not churn the caller's state", () => {
  const onChange = vi.fn();
  wrapTabs("templates", onChange);
  fireEvent.click(screen.getByRole("tab", { name: "Templates" }));
  expect(onChange).not.toHaveBeenCalled();
});
