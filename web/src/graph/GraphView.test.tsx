import { expect, test, vi } from "vitest";
import { fireEvent, render, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { GraphView } from "./GraphView";
import { fromYaml } from "./fromYaml";
import { useGraphStore } from "./store";
import { theme } from "../theme";

test("two read-only graphs isolate destination labels and selection from the builder", () => {
  const document = fromYaml("flows: {main: {nodes: [{id: out, type: publish, destination: queue}]}}");
  useGraphStore.setState({ selectedId: "out", destinations: { queue: { type: "kafka" } } });
  const before = useGraphStore.getState();
  const select = vi.fn();
  const activate = vi.fn();
  const { getByTestId } = render(<MantineProvider theme={theme}>
    <div data-testid="one" style={{ width: 800, height: 400 }}><GraphView mode="readOnly" graph={document}
      destinations={{ queue: { type: "sqs" } }} selectedNodeId={null} selectedEdgeId={null}
      onNodeSelect={select} onNodeActivate={activate} onEdgeSelect={vi.fn()} /></div>
    <div data-testid="two" style={{ width: 800, height: 400 }}><GraphView mode="readOnly" graph={document}
      destinations={{ queue: { type: "rabbitmq" } }} selectedNodeId="out" selectedEdgeId={null}
      onNodeSelect={vi.fn()} onEdgeSelect={vi.fn()} /></div>
  </MantineProvider>);
  const one = getByTestId("one"), two = getByTestId("two");
  expect(within(one).getByText("Send to Amazon SQS")).toBeInTheDocument();
  expect(within(two).getByText("Send to RabbitMQ")).toBeInTheDocument();
  expect((one.querySelector(".mantine-Paper-root") as HTMLElement).style.outline).toBe("");
  expect((two.querySelector(".mantine-Paper-root") as HTMLElement).style.outline).toContain("2px solid");
  expect(within(one).queryByRole("button", { name: "Arrange nodes" })).toBeNull();
  const node = one.querySelector(".react-flow__node")!;
  fireEvent.click(node);
  fireEvent.keyDown(node, { key: "Enter" });
  fireEvent.keyDown(node, { key: " " });
  expect(activate).toHaveBeenCalledTimes(3);
  expect(select).toHaveBeenCalledWith("out");
  expect(useGraphStore.getState()).toBe(before);
});
