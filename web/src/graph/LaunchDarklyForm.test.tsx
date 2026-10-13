import { beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { MantineProvider } from "@mantine/core";
import { LaunchDarklyForm, LaunchDarklyPanel } from "./LaunchDarklyForm";
import { ConditionEditor } from "./ConditionEditor";
import { useGraphStore } from "./store";
import { fromConfig } from "./fromConfig";
import { toConfig } from "./toConfig";

beforeEach(() => useGraphStore.getState().clear());

test("connection form references an environment variable and preserves unrelated configuration", () => {
  useGraphStore
    .getState()
    .setConfigExtra({
      launchdarkly: {
        production: { sdk_key_env: "LD_KEY", future: "retained" },
      },
      custom: 42,
    });
  render(
    <MantineProvider>
      <LaunchDarklyPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.change(screen.getByLabelText("SDK key environment variable"), {
    target: { value: "NEW_LD_KEY" },
  });
  expect(useGraphStore.getState().configExtra).toEqual({
    launchdarkly: {
      production: { sdk_key_env: "NEW_LD_KEY", future: "retained" },
    },
    custom: 42,
  });
  fireEvent.click(screen.getByLabelText("Send evaluation events"));
  expect(
    (
      useGraphStore.getState().configExtra.launchdarkly as Record<
        string,
        Record<string, unknown>
      >
    ).production.send_events,
  ).toBe(false);
});

test("typed fallback edits and literal null survive import/export without changing webhook options", () => {
  const initial = {
    connection: "production",
    name: "rollout",
    context: { key: { source: "body", path: "tenant.id" } },
    flags: {
      settings: {
        key: "tenant.settings",
        type: "json",
        fallback: null,
        variable: "settings",
        future: true,
      },
    },
  };
  let current: Record<string, unknown> = initial;
  function Harness() {
    const [value, set] = useState<Record<string, unknown>>(initial);
    return (
      <LaunchDarklyForm
        nodeType="launchdarkly"
        scope="ld"
        value={value}
        onChange={(v) => {
          current = v;
          set(v);
        }}
      />
    );
  }
  useGraphStore
    .getState()
    .setConfigExtra({
      launchdarkly: { production: { sdk_key_env: "LD_KEY" } },
    });
  render(
    <MantineProvider>
      <Harness />
    </MantineProvider>,
  );
  expect(screen.getByLabelText("Fallback value")).toHaveValue("null");
  fireEvent.change(screen.getByLabelText("Fallback value"), {
    target: { value: '{"limit":2.5,"nested":{"enabled":true}}' },
  });
  const input = {
    launchdarkly: { production: { sdk_key_env: "LD_KEY" } },
    flows: {
      demo: {
        nodes: [{ id: "flags", type: "launchdarkly", ...current }],
        edges: [],
      },
    },
  };
  expect(toConfig(fromConfig(input)).config.flows).toEqual(input.flows);
  fireEvent.change(screen.getByLabelText("Flag type"), {
    target: { value: "number" },
  });
  expect(screen.getByLabelText("Fallback value")).toHaveValue("0");
  fireEvent.change(screen.getByLabelText("Fallback value"), {
    target: { value: "0.001" },
  });
  expect(
    (current.flags as Record<string, Record<string, unknown>>).settings
      .fallback,
  ).toBe(0.001);
  fireEvent.change(screen.getByLabelText("Flag type"), {
    target: { value: "string" },
  });
  expect(screen.getByLabelText("Fallback value")).toHaveValue("");
  expect(
    screen.queryByText("Required to configure this node."),
  ).not.toBeInTheDocument();
  expect(
    (current.flags as Record<string, Record<string, unknown>>).settings
      .fallback,
  ).toBe("");
});

test("flag conditions expose a dedicated source and path guidance", () => {
  render(
    <MantineProvider>
      <ConditionEditor
        single
        value={{
          source: "flags",
          path: "rollout.gate.value",
          operator: "eq",
          value: true,
        }}
        onChange={() => {}}
      />
    </MantineProvider>,
  );
  expect(screen.getByLabelText("Source")).toHaveValue("flags");
  expect(screen.getByLabelText("Flag result path")).toHaveValue(
    "rollout.gate.value",
  );
  expect(screen.getByText(/rollout.settings.value.limit/)).toBeInTheDocument();
});
