import { expect, test, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ExportModal } from "./ExportModal";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep, d], edges: [], selectedId: null, selectedEdgeId: null });
});

test("renders exported YAML for the current graph", () => {
  render(
    <MantineProvider theme={theme}>
      <ExportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  expect(screen.getByText(/max_body_bytes/)).toBeInTheDocument();
  expect(screen.getByText(/\/webhooks\/endpoint_1/)).toBeInTheDocument();
});
