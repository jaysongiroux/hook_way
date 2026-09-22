import { expect, test, beforeEach, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
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
  expect(screen.getByRole("button", { name: "Arrange nodes" })).toBeInTheDocument();
});

test("clicking a node selects it and announces the activation", () => {
  const onNodeActivate = vi.fn();
  const { container } = render(
    <MantineProvider theme={theme}>
      <div style={{ width: 800, height: 600 }}>
        <Canvas onNodeActivate={onNodeActivate} />
      </div>
    </MantineProvider>,
  );

  fireEvent.click(container.querySelector(".react-flow__node")!);

  expect(useGraphStore.getState().selectedId).toBe("http_trigger_1");
  expect(onNodeActivate).toHaveBeenCalledTimes(1);
});

