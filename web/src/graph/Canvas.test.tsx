import { expect, test, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { Canvas } from "./Canvas";
import { useGraphStore } from "./store";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  useGraphStore.setState({
    ...emptyDocument(),
    nodes: [createNode("http_trigger", { x: 0, y: 0 }), createNode("publish", { x: 200, y: 0 })],
    selectedId: null,
  });
});

test("Canvas mounts a React Flow surface fed by the store", () => {
  const { container } = render(
    <MantineProvider theme={theme}>
      <div style={{ width: 800, height: 600 }}>
        <Canvas />
      </div>
    </MantineProvider>,
  );
  expect(container.querySelector(".react-flow")).not.toBeNull();
});
