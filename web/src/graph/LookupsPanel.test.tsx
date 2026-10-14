import { beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { LookupsPanel } from "./LookupsPanel";
import { useGraphStore } from "./store";

beforeEach(() => useGraphStore.getState().clear());
const entries = () =>
  useGraphStore.getState().configExtra.lookups as Record<
    string,
    Record<string, unknown>
  >;

test("lookup providers preserve unrelated settings, reject name collisions, and switch backend explicitly", () => {
  useGraphStore.getState().setConfigExtra({
    lookups: {
      customer: {
        timeout: "3s",
        http: { url: "https://example.test/{key}", value_path: "id" },
        future: true,
      },
    },
    custom: 42,
  });
  render(
    <MantineProvider>
      <LookupsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.change(screen.getByLabelText("Request URL"), {
    target: { value: "https://example.test/users/{key}" },
  });
  expect(entries().customer.http).toEqual({
    url: "https://example.test/users/{key}",
    value_path: "id",
  });
  fireEvent.change(screen.getByLabelText("New lookup provider name"), {
    target: { value: "customer" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add lookup provider" }));
  expect(screen.getByText("That name already exists.")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("combobox", { name: "Lookup backend" }));
  fireEvent.click(screen.getByRole("option", { name: "PostgreSQL" }));
  expect(entries().customer).toEqual({
    timeout: "3s",
    postgres: {},
    future: true,
  });
  expect(useGraphStore.getState().configExtra.custom).toBe(42);
  expect(screen.getByLabelText("Database URL")).toBeInTheDocument();
  expect(screen.queryByLabelText("Result JSON path")).not.toBeInTheDocument();
});

test("removing and renaming providers reconciles invalid drafts", () => {
  const entry = {
    http: { url: "https://example.test/{key}", value_path: "id" },
  };
  useGraphStore.setState({
    configExtra: { lookups: { old: entry } },
    formDrafts: {
      "lookups/old|lookups.old.http.headers": {
        text: "{",
        error: "Invalid JSON",
      },
    },
  });
  useGraphStore.getState().setConfigExtra({ lookups: { renamed: entry } });
  expect(Object.keys(useGraphStore.getState().formDrafts ?? {})).toEqual([
    "lookups/renamed|lookups.renamed.http.headers",
  ]);
  useGraphStore
    .getState()
    .setConfigExtra({ lookups: { renamed: { postgres: {} } } });
  expect(useGraphStore.getState().formDrafts).toEqual({});
});
