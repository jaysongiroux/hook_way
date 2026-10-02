import { expect, test, beforeEach, afterEach, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ExportModal } from "./ExportModal";
import { useGraphStore } from "./store";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({
    ...emptyDocument(),
    nodes: [trigger],
    selectedId: null,
    selectedEdgeId: null,
  });
});

afterEach(() => {
  delete window.hookwayValidate;
  vi.unstubAllGlobals();
});

test("renders exported YAML for the current graph", () => {
  render(
    <MantineProvider theme={theme}>
      <ExportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  expect(screen.getByText(/flows:/)).toBeInTheDocument();
  expect(screen.getByText(/http_trigger/)).toBeInTheDocument();
});

test("exports replay.yaml independently when present", () => {
  useGraphStore.getState().setReplayServiceConfig({
    server: { addr: ":8080" },
    database: { url: "${REPLAY_DATABASE_URL}" },
  });
  render(
    <MantineProvider theme={theme}>
      <ExportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("tab", { name: "Replay YAML" }));
  expect(screen.getByText(/REPLAY_DATABASE_URL/)).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Download replay.yaml" }),
  ).toBeInTheDocument();
});

test("shows when a document could not be validated", async () => {
  window.hookwayValidate = () => {
    throw new Error("bridge failed");
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({ ok: false })),
  );
  render(
    <MantineProvider theme={theme}>
      <ExportModal opened onClose={() => {}} />
    </MantineProvider>,
  );

  expect(
    await screen.findByTestId("validation-unavailable"),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("validation-ok")).not.toBeInTheDocument();
});

test("reports clipboard failure and keeps download available", async () => {
  window.hookwayValidate = () => JSON.stringify({ issues: [] });
  const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
  });
  try {
    render(
      <MantineProvider theme={theme}>
        <ExportModal opened onClose={() => {}} />
      </MantineProvider>,
    );
    await screen.findByTestId("validation-ok");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(
      await screen.findByText(/Could not copy to the clipboard/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Download core.yaml" }),
    ).toBeEnabled();
  } finally {
    if (original) Object.defineProperty(navigator, "clipboard", original);
    else Reflect.deleteProperty(navigator, "clipboard");
  }
});
