import { afterEach, beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { emptyDocument } from "./model";
import { ReplaySettingsPanel } from "./ReplaySettingsPanel";
import { useGraphStore } from "./store";

const replaySchema = {
  type: "object",
  properties: Object.fromEntries(
    ["server", "logging", "metrics", "database", "core", "retention", "workers"].map((key) => [key, { type: "object", properties: {} }]),
  ),
};

beforeEach(() => {
  useGraphStore.setState({ ...emptyDocument(), selectedId: null, selectedEdgeId: null });
  window.hookwayValidate = () => JSON.stringify({ issues: [] });
  window.hookwayReplaySchema = () => JSON.stringify(replaySchema);
  window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities: ["core_yaml_validate", "replay_schema"] });
});

afterEach(() => {
  delete window.hookwayValidate;
  delete window.hookwayReplaySchema;
  delete window.hookwayContractInfo;
});

test("adds coordinated defaults and masks core tokens", async () => {
  render(<MantineProvider theme={theme}><ReplaySettingsPanel opened onClose={() => {}} /></MantineProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Add replay configuration" }));
  expect(useGraphStore.getState().coreReplayConfig).toMatchObject({ capture_token: "${REPLAY_CAPTURE_TOKEN}" });
  expect(useGraphStore.getState().replayServiceConfig).toMatchObject({ workers: { concurrency: 4 } });
  const capture = await screen.findByLabelText("capture token");
  expect(capture).toHaveAttribute("type", "password");
});

test("warns when shared credentials drift", async () => {
  useGraphStore.setState({
    coreReplayConfig: { capture_token: "${CORE_CAPTURE}", control: { token: "${CORE_CONTROL}" } },
    replayServiceConfig: {
      core: { control_token: "${REPLAY_CONTROL}" },
      keys: [{ name: "core", token: "${REPLAY_CAPTURE}", scopes: ["capture"] }],
    },
  });
  render(<MantineProvider theme={theme}><ReplaySettingsPanel opened onClose={() => {}} /></MantineProvider>);
  await waitFor(() => expect(screen.getAllByText("Check shared credentials")).toHaveLength(2));
});
