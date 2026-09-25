import { beforeEach, expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { EnvPanel } from "./EnvPanel";
import { emptyDocument } from "./model";
import { useGraphStore } from "./store";

beforeEach(() => useGraphStore.setState({ ...emptyDocument(), selectedId: null, selectedEdgeId: null }));

test("replay variables remain visible when core cannot be exported", () => {
  useGraphStore.setState({
    edges: [{ id: "broken", source: "missing", target: "also_missing" }],
    replayServiceConfig: { database: { url: "${REPLAY_DATABASE_URL}" } },
  });
  render(<MantineProvider theme={theme}><EnvPanel opened onClose={() => {}} /></MantineProvider>);
  expect(screen.getByRole("tab", { name: "Replay (1)" })).toBeInTheDocument();
  expect(screen.getByText("REPLAY_DATABASE_URL")).toBeInTheDocument();
});
