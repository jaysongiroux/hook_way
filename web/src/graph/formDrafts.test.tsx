import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { beforeEach, expect, test, vi } from "vitest";
import { theme } from "../theme";
import { SchemaForm } from "./SchemaForm";
import { draftKey } from "./formDrafts";
import { emptyDocument } from "./model";
import type { JSONSchema } from "./nodeSchema";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";

const NODE_SCOPE = "node/main/node_1";

beforeEach(() => useGraphStore.setState(emptyDocument()));

function StatefulForm({
  schema,
  initialValue,
  path = "options",
  onChange = () => undefined,
}: {
  schema: JSONSchema;
  initialValue: unknown;
  path?: string;
  onChange?: (value: unknown) => void;
}) {
  const [value, setValue] = useState(initialValue);
  const [mounted, setMounted] = useState(true);
  return (
    <>
      {mounted ? (
        <SchemaForm
          schema={schema}
          value={value}
          onChange={(next) => {
            setValue(next);
            onChange(next);
          }}
          scope={NODE_SCOPE}
          path={path}
        />
      ) : (
        <button onClick={() => setMounted(true)}>Remount form</button>
      )}
      <button onClick={() => setMounted(false)}>Unmount form</button>
      <output data-testid="form-value">{JSON.stringify(value)}</output>
    </>
  );
}

function renderForm(
  schema: JSONSchema,
  initialValue: unknown,
  onChange?: (value: unknown) => void,
) {
  return render(
    <MantineProvider theme={theme}>
      <StatefulForm
        schema={schema}
        initialValue={initialValue}
        onChange={onChange}
      />
    </MantineProvider>,
  );
}

function currentValue() {
  return JSON.parse(
    screen.getByTestId("form-value").textContent ?? "null",
  ) as Record<string, unknown>;
}

const freeJsonSchema: JSONSchema = {
  type: "object",
  properties: { settings: { type: "object", additionalProperties: true } },
};

test("invalid JSON survives form unmount and remount, and revert clears its scoped draft", () => {
  renderForm(freeJsonSchema, { settings: { version: 1 } });
  const input = screen.getByRole("textbox", { name: "settings" });
  fireEvent.change(input, { target: { value: '{"version":' } });
  const key = draftKey(NODE_SCOPE, "options.settings");
  expect(useGraphStore.getState().formDrafts?.[key]).toMatchObject({
    error: expect.stringMatching(/valid JSON/i),
    text: '{"version":',
  });
  expect(currentValue()).toEqual({ settings: { version: 1 } });

  fireEvent.click(screen.getByRole("button", { name: "Unmount form" }));
  fireEvent.click(screen.getByRole("button", { name: "Remount form" }));
  expect(screen.getByRole("textbox", { name: "settings" })).toHaveValue(
    '{"version":',
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Revert settings draft" }),
  );
  expect(useGraphStore.getState().formDrafts?.[key]).toBeUndefined();
  expect(screen.getByRole("textbox", { name: "settings" })).toHaveValue(
    JSON.stringify({ version: 1 }, null, 2),
  );
});

test("form drafts remain editor-only and never appear in exported config", () => {
  useGraphStore
    .getState()
    .setFormDraft(draftKey(NODE_SCOPE, "options.settings"), {
      text: '{"broken":',
      error: "Invalid JSON",
    });
  const config = toConfig(useGraphStore.getState().document()).config;
  expect(config).not.toHaveProperty("formDrafts");
  expect(JSON.stringify(config)).not.toContain("Invalid JSON");
  expect(JSON.stringify(config)).not.toContain("broken");
});

test("duplicate map keys retain both draft rows without overwriting the last valid map", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: {
      headers: { type: "object", additionalProperties: { type: "string" } },
    },
  };
  renderForm(schema, { headers: { First: "one", Second: "two" } });

  fireEvent.change(screen.getByLabelText("Key 2"), {
    target: { value: "First" },
  });
  expect(screen.getAllByRole("textbox", { name: /Key/ })).toHaveLength(2);
  expect(
    screen.getByText(/Duplicate keys: give each entry a unique key/i),
  ).toBeInTheDocument();
  expect(
    useGraphStore.getState().formDrafts?.[
      draftKey(NODE_SCOPE, "options.headers")
    ],
  ).toMatchObject({ error: expect.stringMatching(/Duplicate keys/i) });
  expect(currentValue()).toEqual({ headers: { First: "one", Second: "two" } });

  fireEvent.change(screen.getByLabelText("Key 2"), {
    target: { value: "Third" },
  });
  expect(currentValue()).toEqual({ headers: { First: "one", Third: "two" } });
  expect(screen.queryByText(/Duplicate keys/i)).not.toBeInTheDocument();
  expect(
    useGraphStore.getState().formDrafts?.[
      draftKey(NODE_SCOPE, "options.headers")
    ]?.error,
  ).toBeFalsy();

  fireEvent.change(screen.getByLabelText("Key 2"), {
    target: { value: "First" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Revert entries draft" }));
  expect(
    useGraphStore.getState().formDrafts?.[
      draftKey(NODE_SCOPE, "options.headers")
    ],
  ).toBeUndefined();
  expect(screen.queryByText(/Duplicate keys/i)).not.toBeInTheDocument();
  expect(currentValue()).toEqual({ headers: { First: "one", Third: "two" } });
});

test("disabling an optional object removes its nested invalid JSON draft", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: {
      credential: {
        type: "object",
        properties: {
          settings: { type: "object", additionalProperties: true },
        },
      },
    },
  };
  renderForm(schema, { credential: { settings: { mode: "safe" } } });
  fireEvent.change(screen.getByRole("textbox", { name: "settings" }), {
    target: { value: '{"mode":' },
  });
  const key = draftKey(NODE_SCOPE, "options.credential.settings");
  expect(useGraphStore.getState().formDrafts?.[key]).toBeDefined();

  fireEvent.click(screen.getByRole("switch", { name: "Use credential" }));
  expect(currentValue()).toEqual({});
  expect(useGraphStore.getState().formDrafts?.[key]).toBeUndefined();
  expect(
    screen.queryByRole("textbox", { name: "settings" }),
  ).not.toBeInTheDocument();
});

test("an absent boolean with a true default displays its effective value without writing it", () => {
  const onChange = vi.fn();
  const schema: JSONSchema = {
    type: "object",
    properties: { enabled: { type: "boolean", default: true } },
  };
  renderForm(schema, {}, onChange);

  expect(screen.getByRole("switch", { name: "enabled" })).toBeChecked();
  expect(screen.getByText("Using default: on.")).toBeInTheDocument();
  expect(onChange).not.toHaveBeenCalled();
  expect(currentValue()).toEqual({});
});

test("explicit false overrides a true default", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: { enabled: { type: "boolean", default: true } },
  };
  renderForm(schema, { enabled: false });

  expect(screen.getByRole("switch", { name: "enabled" })).not.toBeChecked();
  expect(screen.getByText("Explicit override.")).toBeInTheDocument();
});

test("removing an earlier object row preserves the following row draft across reopening", async () => {
  const schema: JSONSchema = {
    type: "object",
    properties: {
      records: {
        type: "array",
        items: {
          type: "object",
          properties: {
            settings: { type: "object", additionalProperties: true },
          },
        },
      },
    },
  };
  renderForm(schema, {
    records: [{ settings: { row: 1 } }, { settings: { row: 2 } }],
  });
  await waitFor(() =>
    expect(
      useGraphStore.getState().formDrafts?.[
        draftKey(NODE_SCOPE, "options.records._rows")
      ],
    ).toBeDefined(),
  );
  const rowIds =
    useGraphStore.getState().formDrafts?.[
      draftKey(NODE_SCOPE, "options.records._rows")
    ]?.rowIds;
  expect(rowIds).toHaveLength(2);
  const secondRowScope = `${NODE_SCOPE}/${rowIds![1]}`;

  const textareas = screen.getAllByRole("textbox", { name: "settings" });
  fireEvent.change(textareas[1], { target: { value: '{"row":' } });
  const secondDraftKey = draftKey(secondRowScope, "options.records.settings");
  expect(useGraphStore.getState().formDrafts?.[secondDraftKey]?.error).toMatch(
    /valid JSON/i,
  );

  fireEvent.click(screen.getByRole("button", { name: "Remove records 1" }));
  expect(currentValue().records as unknown[]).toHaveLength(1);
  expect(useGraphStore.getState().formDrafts?.[secondDraftKey]?.text).toBe(
    '{"row":',
  );
  fireEvent.click(screen.getByRole("button", { name: "Unmount form" }));
  fireEvent.click(screen.getByRole("button", { name: "Remount form" }));
  expect(screen.getByRole("textbox", { name: "settings" })).toHaveValue(
    '{"row":',
  );
  expect(useGraphStore.getState().formDrafts?.[secondDraftKey]?.error).toMatch(
    /valid JSON/i,
  );
});
