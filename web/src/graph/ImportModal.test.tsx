import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ImportModal } from "./ImportModal";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";

beforeEach(() => useGraphStore.setState({ ...emptyDocument(), selectedId: null, selectedEdgeId: null }));

test("importing flow YAML populates the store graph", () => {
  render(
    <MantineProvider theme={theme}>
      <ImportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  const yaml = `server: { addr: ":8080", max_body_bytes: 1048576 }
destinations:
  d: { type: webhook, webhook: { url: https://example.test } }
flows:
  a:
    nodes:
      - {id: in, type: http_trigger, path: /a, methods: [POST]}
      - {id: out, type: publish, destination: d, target: t}
    edges: [in>out]
`;
  fireEvent.change(screen.getByPlaceholderText("Paste core.yaml here"), { target: { value: yaml } });
  fireEvent.click(screen.getByText("Import core YAML"));
  expect(useGraphStore.getState().nodes.map((n) => n.id).sort()).toEqual(["in", "out"]);
  expect(useGraphStore.getState().activeFlow).toBe("a");
});

test("replay import replaces only replay.yaml", () => {
  useGraphStore.setState({
    ...emptyDocument(),
    coreReplayConfig: { url: "http://old-replay:8080" },
    nodes: [{
      id: "in",
      type: "flow",
      position: { x: 0, y: 0 },
      data: { nodeType: "http_trigger", yamlId: "in", options: { path: "/kept" } },
    }],
    selectedId: null,
    selectedEdgeId: null,
  });
  render(<MantineProvider theme={theme}><ImportModal opened onClose={() => {}} /></MantineProvider>);
  fireEvent.click(screen.getByRole("tab", { name: "Replay YAML" }));
  fireEvent.change(screen.getByPlaceholderText("Paste replay.yaml here"), {
    target: { value: "server: {addr: ':8080'}\ndatabase: {url: '${DB}'}\n" },
  });
  fireEvent.click(screen.getByText("Import replay YAML"));
  expect(useGraphStore.getState().nodes[0].id).toBe("in");
  expect(useGraphStore.getState().coreReplayConfig).toEqual({ url: "http://old-replay:8080" });
  expect(useGraphStore.getState().replayServiceConfig).toEqual({ server: { addr: ":8080" }, database: { url: "${DB}" } });
});
