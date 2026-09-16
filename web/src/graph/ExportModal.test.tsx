import { expect, test, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ExportModal } from "./ExportModal";
import { useGraphStore } from "./store";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.setState({ ...emptyDocument(), nodes: [trigger], selectedId: null, selectedEdgeId: null });
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
