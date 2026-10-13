import { useState } from "react";
import { beforeEach, expect, test } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { SchemaForm } from "./SchemaForm";
import type { JSONSchema } from "./nodeSchema";
import { emptyDocument } from "./model";
import { useGraphStore } from "./store";

beforeEach(() => useGraphStore.setState(emptyDocument()));

function wrap(
  schema: JSONSchema,
  value: unknown,
  onChange: (v: unknown) => void,
  fieldHelp?: (name: string) => string | undefined,
) {
  return render(
    <MantineProvider theme={theme}>
      <SchemaForm
        schema={schema}
        value={value}
        onChange={onChange}
        fieldHelp={fieldHelp}
      />
    </MantineProvider>,
  );
}

test("renders string, number, boolean, enum, array, nested object, and required marking", () => {
  const schema: JSONSchema = {
    type: "object",
    required: ["name"],
    properties: {
      name: { type: "string" },
      count: { type: "integer", default: 3 },
      enabled: { type: "boolean" },
      color: { type: "string", enum: ["red", "blue"] },
      tags: { type: "array", items: { type: "string" } },
      retry: {
        type: "object",
        properties: { max_attempts: { type: "integer" } },
      },
    },
  };
  wrap(
    schema,
    {
      name: "",
      count: "",
      enabled: false,
      color: "red",
      tags: ["a"],
      retry: { max_attempts: 1 },
    },
    () => undefined,
  );
  expect(screen.getByLabelText("name")).toBeRequired();
  expect(screen.getByLabelText("count")).toBeInTheDocument();
  expect(screen.getByLabelText("enabled")).toBeInTheDocument();
  expect(screen.getByLabelText("color")).toBeInTheDocument();
  expect(screen.getByLabelText("tags")).toBeInTheDocument();
  expect(screen.getByText("retry")).toBeInTheDocument();
  expect(screen.getByLabelText("max attempts")).toBeInTheDocument();
  expect(screen.getByPlaceholderText("Default: 3")).toBeInTheDocument();
});

test("shows field help under an input", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: { path: { type: "string" } },
  };
  wrap(
    schema,
    { path: "/x" },
    () => undefined,
    () => "Public URL path.",
  );
  expect(screen.getByText("Public URL path.")).toBeInTheDocument();
});

test("shows a validation message on blur when a required field is empty", () => {
  const schema: JSONSchema = {
    type: "object",
    required: ["path"],
    properties: { path: { type: "string" } },
  };
  wrap(schema, { path: "" }, () => undefined);
  const input = screen.getByLabelText("path");
  fireEvent.blur(input);
  expect(
    screen.getByText("Required to configure this node."),
  ).toBeInTheDocument();
});

const GROUPED_SCHEMA: JSONSchema = {
  type: "object",
  properties: {
    url: { type: "string" },
    timeout: { type: "string" },
    retry: {
      type: "object",
      properties: {
        max_attempts: { type: "integer" },
        max_backoff: { type: "string" },
      },
    },
    tls: { type: "object", properties: { ca_cert: { type: "string" } } },
  },
};

function wrapGrouped(
  value: unknown,
  onChange: (v: unknown) => void = () => undefined,
) {
  function StatefulGroupedForm() {
    const [current, setCurrent] = useState(value);
    return (
      <SchemaForm
        schema={GROUPED_SCHEMA}
        value={current}
        onChange={(next) => {
          setCurrent(next);
          onChange(next);
        }}
        groupObjects
      />
    );
  }
  return render(
    <MantineProvider theme={theme}>
      <StatefulGroupedForm />
    </MantineProvider>,
  );
}

function block(name: string) {
  return screen.getByRole("button", { name: new RegExp(`^${name}`, "i") });
}

test("groupObjects keeps scalars flat and folds each nested block away", () => {
  wrapGrouped({ url: "https://x", retry: { max_attempts: 3 } });

  expect(screen.getByLabelText("url")).toHaveValue("https://x");
  expect(screen.getByLabelText("timeout")).toBeInTheDocument();
  expect(screen.queryByLabelText("max attempts")).not.toBeInTheDocument();

  fireEvent.click(block("retry"));

  expect(screen.getByLabelText("max attempts")).toHaveValue("3");
});

test("a folded block reports whether it is configured", () => {
  wrapGrouped({ retry: { max_attempts: 3, max_backoff: "1m" } });

  expect(block("retry")).toHaveTextContent("2 set");
  expect(block("tls")).toHaveTextContent("not set");
});

test("editing inside a folded block writes through to the whole value", () => {
  const changes: unknown[] = [];
  wrapGrouped({ url: "https://x" }, (v) => changes.push(v));

  fireEvent.click(block("tls"));
  expect(screen.queryByLabelText("ca cert")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("switch", { name: "Use tls" }));
  fireEvent.change(screen.getByLabelText("ca cert"), {
    target: { value: "/etc/ca.pem" },
  });

  expect(changes.at(-1)).toEqual({
    url: "https://x",
    tls: { ca_cert: "/etc/ca.pem" },
  });
});

test("an expanded block stays a labelled group, as the fieldset it replaced was", () => {
  wrapGrouped({ retry: { max_attempts: 3 } });

  fireEvent.click(block("retry"));

  const group = screen.getByRole("group", { name: "retry" });
  expect(within(group).getByLabelText("max attempts")).toHaveValue("3");
});

test("free-form schemas and child node maps remain editable JSON, with invalid drafts retained", () => {
  const changes: unknown[] = [];
  const value = {
    schema: { type: "object", required: ["id"] },
    nodes: [{ id: "batch.send", type: "publish", destination: "queue" }],
  };
  wrap(
    {
      type: "object",
      properties: {
        schema: { type: "object", additionalProperties: true },
        nodes: {
          type: "array",
          items: { type: "object", additionalProperties: true },
        },
      },
    },
    value,
    (next) => changes.push(next),
  );
  const schema = screen.getByRole("textbox", { name: "schema" });
  expect(JSON.parse((schema as HTMLTextAreaElement).value)).toEqual(
    value.schema,
  );
  expect(
    JSON.parse(
      (screen.getByRole("textbox", { name: "nodes" }) as HTMLTextAreaElement)
        .value,
    ),
  ).toEqual(value.nodes[0]);
  fireEvent.change(schema, { target: { value: '{"type":' } });
  expect(changes).toHaveLength(0);
  expect(schema).toHaveValue('{"type":');
  fireEvent.change(schema, { target: { value: '{"type":"string"}' } });
  expect(changes).toEqual([{ ...value, schema: { type: "string" } }]);
  fireEvent.change(screen.getByRole("textbox", { name: "nodes" }), {
    target: {
      value: '{"id":"batch.send","type":"publish","destination":"other"}',
    },
  });
  expect(changes.at(-1)).toEqual({
    ...value,
    nodes: [{ id: "batch.send", type: "publish", destination: "other" }],
  });
});
