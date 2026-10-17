import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { expect, test } from "vitest";
import { MCPOptionsForm } from "./MCPOptionsForm";
import { defaultNodeOptions, labelledPorts } from "./nodeSchema";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";
import { templateSubgraph } from "./templates";
import { toConfig } from "./toConfig";
import { fromConfig } from "./fromConfig";

test("malformed imported MCP tools remain editable and survive export", () => {
  const tools = [null, "invalid", 17, [], {}, { name: null }, { name: "valid" }];
  const doc = fromConfig({
    schema_version: 2,
    flows: { gateway: { nodes: [{ id: "mcp", type: "mcp_trigger", tools }], edges: [] } },
  });
  expect(labelledPorts("mcp_trigger", doc.nodes[0].data.options)).toEqual(["valid"]);
  expect(toConfig(doc).config).toMatchObject({
    flows: { gateway: { nodes: [{ tools }] } },
  });
});

test("the live tester follows endpoint changes and clears the previous endpoint token", () => {
  const options = defaultNodeOptions("mcp_trigger", "mcp");
  function Example({ endpoint }: { endpoint: string }) {
    return (
      <MantineProvider>
        <MCPOptionsForm
          nodeType="mcp_trigger"
          nodeId="mcp"
          value={{ ...options, public_url: endpoint }}
          onChange={() => {}}
          scope="mcp-endpoint-test"
        />
      </MantineProvider>
    );
  }
  const { rerender } = render(<Example endpoint="https://first.example/mcp" />);
  fireEvent.click(screen.getByRole("tab", { name: "Test & connect" }));
  expect(screen.getByLabelText("Deployed MCP URL")).toHaveValue("https://first.example/mcp");
  fireEvent.change(screen.getByLabelText("API key or OAuth access token"), {
    target: { value: "first-endpoint-token" },
  });
  rerender(<Example endpoint="https://second.example/mcp" />);
  expect(screen.getByLabelText("Deployed MCP URL")).toHaveValue("https://second.example/mcp");
  expect(screen.getByLabelText("API key or OAuth access token")).toHaveValue("");
  expect(screen.getByRole("button", { name: "Connect and discover" })).toBeDisabled();
});

test("MCP authoring starts authenticated and adds named tool outputs", () => {
  function Example() {
    const [value, setValue] = useState(
      defaultNodeOptions("mcp_trigger", "mcp"),
    );
    return (
      <MantineProvider>
        <MCPOptionsForm
          nodeType="mcp_trigger"
          nodeId="mcp"
          value={value}
          onChange={setValue}
          scope="mcp-test"
        />
      </MantineProvider>
    );
  }
  render(<Example />);
  expect(
    screen.getByRole("combobox", { name: "Client authentication" }),
  ).toHaveValue("API keys (recommended for a simple setup)");
  fireEvent.click(screen.getByRole("tab", { name: "Tools (0)" }));
  fireEvent.click(screen.getByRole("button", { name: "Add tool" }));
  expect(screen.getByRole("tab", { name: "Tools (1)" })).toBeVisible();
  expect(screen.getByLabelText("Tool name")).toHaveValue("tool_1");
  expect(screen.getByLabelText("Input JSON schema")).toBeVisible();
  expect(screen.getByLabelText("Output JSON schema")).toBeVisible();
  expect(
    labelledPorts("mcp_trigger", {
      tools: [{ name: "first" }, { name: "second" }],
    }),
  ).toEqual(["first", "second"]);
  fireEvent.click(screen.getByRole("button", { name: "Add tool" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove tool 1" }));
  fireEvent.click(screen.getByRole("button", { name: "Add tool" }));
  expect(
    screen
      .getAllByLabelText("Tool name")
      .map((input) => (input as HTMLInputElement).value),
  ).toEqual(["tool_2", "tool_3"]);
});

test("complete MCP template creates a separate flow and repairs credential collisions", () => {
  const doc = emptyDocument();
  useGraphStore.setState({
    ...doc,
    configExtra: {
      credentials: {
        service_status: {
          kind: "static",
          allowed_origins: ["https://existing.example"],
        },
      },
    },
  });
  const first = templateSubgraph("mcp-gateway")!;
  useGraphStore.getState().insertSubgraph(first, { x: 0, y: 0 });
  const state = useGraphStore.getState();
  expect(state.configExtra.credentials).toHaveProperty("service_status_2");
  expect(
    state.nodes.find((n) => n.data.nodeType === "http_request")?.data.options
      .credential,
  ).toEqual({ profile: "service_status_2" });
  expect(state.flowExtra?.capture_failures).toBe(false);
  const exported = toConfig(state).config as {
    flows: Record<string, { nodes: Record<string, unknown>[] }>;
  };
  expect(
    exported.flows[state.activeFlow].nodes.find(
      (n) => n.type === "mcp_trigger",
    ),
  ).toHaveProperty("tools");
  useGraphStore
    .getState()
    .insertSubgraph(templateSubgraph("mcp-gateway")!, { x: 0, y: 0 });
  const second = useGraphStore.getState();
  expect(second.activeFlow).not.toBe(state.activeFlow);
  expect(
    second.nodes.find((node) => node.data.nodeType === "mcp_trigger")?.data
      .options,
  ).toMatchObject({
    path: "/mcp-2",
    public_url: "https://hookway.example.com/mcp-2",
  });
  expect(second.otherFlows[state.activeFlow].nodes).toHaveLength(5);
  for (const [flow, path] of [
    [state.activeFlow, "/services/mcp"],
    [second.activeFlow, "/utilities/mcp"],
  ]) {
    useGraphStore.getState().setActiveFlow(flow);
    const trigger = useGraphStore
      .getState()
      .nodes.find((node) => node.data.nodeType === "mcp_trigger")!;
    useGraphStore.getState().updateNodeData(trigger.id, {
      options: {
        ...trigger.data.options,
        path,
        public_url: `https://gateway.example${path}`,
      },
    });
  }
  const config = toConfig(useGraphStore.getState()).config as {
    flows: Record<string, { nodes: Record<string, unknown>[] }>;
  };
  expect(
    Object.values(config.flows)
      .flatMap((flow) =>
        flow.nodes
          .filter((node) => node.type === "mcp_trigger")
          .map((node) => node.path),
      )
      .sort(),
  ).toEqual(["/services/mcp", "/utilities/mcp"]);
});

test("adding MCP triggers creates separate flows and chooses an unused endpoint path", () => {
  useGraphStore.setState(emptyDocument());
  const trigger = templateSubgraph("mcp-gateway")!.nodes.find(
    (node) => node.data.nodeType === "mcp_trigger",
  )!;
  useGraphStore.getState().addNode(trigger);
  const firstFlow = useGraphStore.getState().activeFlow;
  useGraphStore.getState().addNode(trigger);
  const state = useGraphStore.getState();
  expect(state.activeFlow).not.toBe(firstFlow);
  expect(state.otherFlows[firstFlow].nodes[0].data.options.path).toBe("/mcp");
  expect(state.nodes[0].data.options.path).toBe("/mcp-2");
});

test("renaming tool handles preserves connections and removing a row preserves another tool's invalid draft", () => {
  const sub = templateSubgraph("mcp-gateway")!;
  useGraphStore.setState({
    ...emptyDocument(),
    nodes: sub.nodes.map((node) =>
      node.id === "mcp" ? { ...node, id: "internal-mcp" } : node,
    ),
    edges: sub.edges.map((edge) =>
      edge.source === "mcp" ? { ...edge, source: "internal-mcp" } : edge,
    ),
  });
  function Editor() {
    const node = useGraphStore((state) =>
      state.nodes.find((node) => node.data.nodeType === "mcp_trigger")!,
    );
    return (
      <MantineProvider>
        <MCPOptionsForm
          nodeType="mcp_trigger"
          nodeId="mcp"
          scope="mcp-row-test"
          value={node.data.options}
          onChange={(options) =>
            useGraphStore.getState().updateNodeData(node.id, { options })
          }
        />
      </MantineProvider>
    );
  }
  render(<Editor />);
  fireEvent.click(screen.getByRole("tab", { name: "Tools (2)" }));
  expect(
    screen.getByRole("button", { name: /Connected REST operation/ }),
  ).toBeVisible();
  fireEvent.change(screen.getAllByLabelText("Tool name")[0], {
    target: { value: "format_greeting" },
  });
  expect(screen.getByText(/Another tool already uses this name/)).toBeVisible();
  const trigger = useGraphStore
    .getState()
    .nodes.find((node) => node.id === "internal-mcp")!;
  expect((trigger.data.options.tools as { name: string }[])[0].name).toBe(
    "get_service_status",
  );
  fireEvent.change(screen.getAllByLabelText("Tool name")[0], {
    target: { value: "service_health" },
  });
  expect(
    useGraphStore
      .getState()
      .edges.some(
        (edge) =>
          edge.source === "internal-mcp" &&
          edge.sourceHandle === "service_health",
      ),
  ).toBe(true);
  expect(
    useGraphStore
      .getState()
      .edges.some(
        (edge) =>
          edge.sourceHandle === "format_greeting" && edge.target === "greeting",
      ),
  ).toBe(true);
  fireEvent.change(screen.getAllByLabelText("Input JSON schema")[1], {
    target: { value: "{invalid" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Remove tool 1" }));
  expect(screen.getByLabelText("Tool name")).toHaveValue("format_greeting");
  expect(screen.getByLabelText("Input JSON schema")).toHaveValue("{invalid");
  expect(
    useGraphStore
      .getState()
      .edges.some((edge) => edge.sourceHandle === "service_health"),
  ).toBe(false);
});
