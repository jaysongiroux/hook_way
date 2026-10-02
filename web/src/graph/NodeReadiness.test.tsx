import { beforeEach, expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { emptyDocument, createNode } from "./model";
import { nodeScope } from "./formDrafts";
import { NodeReadiness } from "./NodeReadiness";
import { useGraphStore } from "./store";

beforeEach(() => {
  useGraphStore.setState(emptyDocument());
});

test("fallback validation shows drafts owned by this node and nested child scopes only", () => {
  const node = createNode("http_trigger", { x: 0, y: 0 });
  node.id = "auth_1";
  const parentScope = nodeScope("main", node.id);
  useGraphStore.setState({
    ...emptyDocument(),
    nodes: [node],
    formDrafts: {
      [`${parentScope}|options.headers`]: { text: "{", error: "Invalid JSON" },
      [`${parentScope}/child|options.body`]: {
        text: "{",
        error: "Invalid child JSON",
      },
      [`${nodeScope("main", "auth_10")}|options.headers`]: {
        text: "{",
        error: "Sibling error",
      },
      [`${parentScope}ish|options.headers`]: {
        text: "{",
        error: "Unrelated error",
      },
    },
  });

  render(
    <MantineProvider>
      <NodeReadiness nodeId={node.id} />
    </MantineProvider>,
  );

  expect(screen.getByText("Invalid JSON")).toBeInTheDocument();
  expect(screen.getByText("Invalid child JSON")).toBeInTheDocument();
  expect(screen.queryByText("Sibling error")).not.toBeInTheDocument();
  expect(screen.queryByText("Unrelated error")).not.toBeInTheDocument();
});
