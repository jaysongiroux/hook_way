import { expect, test } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { SchemaForm } from "./SchemaForm";
import type { JSONSchema } from "./nodeSchema";

function wrap(schema: JSONSchema, value: unknown, onChange: (v: unknown) => void) {
  return render(
    <MantineProvider theme={theme}>
      <SchemaForm schema={schema} value={value} onChange={onChange} />
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
      retry: { type: "object", properties: { max_attempts: { type: "integer" } } },
    },
  };
  wrap(schema, { name: "", count: "", enabled: false, color: "red", tags: ["a"], retry: { max_attempts: 1 } }, () => undefined);
  expect(screen.getByLabelText("name")).toBeRequired();
  expect(screen.getByLabelText("count")).toBeInTheDocument();
  expect(screen.getByLabelText("enabled")).toBeInTheDocument();
  expect(screen.getByLabelText("color")).toBeInTheDocument();
  expect(screen.getByLabelText("tags")).toBeInTheDocument();
  expect(screen.getByText("retry")).toBeInTheDocument();
  expect(screen.getByLabelText("max attempts")).toBeInTheDocument();
  expect(screen.getByPlaceholderText("3")).toBeInTheDocument();
});

test("shows a validation message on blur when a required field is empty", () => {
  const schema: JSONSchema = { type: "object", required: ["path"], properties: { path: { type: "string" } } };
  wrap(schema, { path: "" }, () => undefined);
  const input = screen.getByLabelText("path");
  fireEvent.blur(input);
  expect(screen.getByText("Required")).toBeInTheDocument();
});
