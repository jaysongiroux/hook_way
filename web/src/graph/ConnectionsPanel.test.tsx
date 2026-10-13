import { expect, test, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ConnectionsPanel } from "./ConnectionsPanel";
import { useGraphStore } from "./store";

function wrap(onClose = vi.fn()) {
  return render(
    <MantineProvider theme={theme}>
      <ConnectionsPanel opened onClose={onClose} />
    </MantineProvider>,
  );
}

function setConnections(connections: Record<string, unknown>) {
  useGraphStore.setState({ connections });
}

function rail() {
  return screen.getByRole("navigation", { name: "Connections" });
}

function editor(name: string) {
  return screen.getByTestId(`connection-${name}`);
}

beforeEach(() => {
  useGraphStore.getState().clear();
});

test("lists every connection in the rail and edits the first one", () => {
  setConnections({
    aws_prod: { type: "aws", region: "us-east-1" },
    pg_main: { type: "postgres", url: "postgres://db/x" },
  });
  wrap();

  expect(within(rail()).getByRole("button", { name: "Select aws_prod" })).toBeInTheDocument();
  expect(within(rail()).getByRole("button", { name: "Select pg_main" })).toBeInTheDocument();
  expect(within(editor("aws_prod")).getByLabelText("region")).toHaveValue("us-east-1");
  expect(screen.queryByTestId("connection-pg_main")).not.toBeInTheDocument();
});

test("picking a rail row swaps which connection the editor holds", () => {
  setConnections({
    aws_prod: { type: "aws", region: "us-east-1" },
    pg_main: { type: "postgres", url: "postgres://db/x" },
  });
  wrap();

  fireEvent.click(within(rail()).getByRole("button", { name: "Select pg_main" }));

  expect(within(editor("pg_main")).getByLabelText("url")).toHaveValue("postgres://db/x");
});

test("an empty config opens on the type grid instead of a blank pane", () => {
  wrap();
  expect(screen.getByRole("button", { name: "Add aws connection" })).toBeInTheDocument();
});

test("choosing a type from the grid creates a flat entry with only its type", () => {
  wrap();

  fireEvent.click(screen.getByRole("button", { name: "Add postgres connection" }));

  // Flat, not nested under a per-type block: that is how connections are
  // authored, and how internal/config decodes them.
  expect(useGraphStore.getState().connections).toEqual({ postgres: { type: "postgres" } });
  expect(editor("postgres")).toBeInTheDocument();
});

test("a second connection of the same type gets a suffixed name", () => {
  setConnections({ aws: { type: "aws" } });
  wrap();

  fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
  fireEvent.click(screen.getByRole("button", { name: "Add aws connection" }));

  expect(Object.keys(useGraphStore.getState().connections)).toEqual(["aws", "aws_2"]);
});

test("editing a field writes it flat alongside the type", () => {
  setConnections({ aws_prod: { type: "aws" } });
  wrap();

  fireEvent.change(within(editor("aws_prod")).getByLabelText("region"), { target: { value: "eu-west-2" } });

  expect(useGraphStore.getState().connections).toEqual({ aws_prod: { type: "aws", region: "eu-west-2" } });
});

test("renaming commits on blur and keeps the connection in place", () => {
  setConnections({
    a: { type: "aws", region: "us-east-1" },
    b: { type: "postgres", url: "postgres://db/x" },
    c: { type: "kafka" },
  });
  wrap();
  fireEvent.click(within(rail()).getByRole("button", { name: "Select b" }));

  const name = within(editor("b")).getByLabelText("Name");
  fireEvent.change(name, { target: { value: "pg_main" } });
  fireEvent.blur(name);

  expect(Object.keys(useGraphStore.getState().connections)).toEqual(["a", "pg_main", "c"]);
  expect(useGraphStore.getState().connections.pg_main).toEqual({ type: "postgres", url: "postgres://db/x" });
});

// internal/config rejects a dot in a connection name, because issue field
// paths are resolved segment by segment and a dot makes them ambiguous. The
// form should say so rather than exporting config that fails to load.
test("a name with a dot is rejected rather than committed", () => {
  setConnections({ aws_prod: { type: "aws" } });
  wrap();

  const name = within(editor("aws_prod")).getByLabelText("Name");
  fireEvent.change(name, { target: { value: "aws.prod" } });
  expect(screen.getByText("Use letters, numbers, underscore, or hyphen only.")).toBeInTheDocument();

  fireEvent.blur(name);
  expect(Object.keys(useGraphStore.getState().connections)).toEqual(["aws_prod"]);
});

test("switching type starts the new type empty", () => {
  // Field names overlap across types (url, tls) but their legal values do not,
  // so carrying values over would export config the validator rejects.
  setConnections({ c: { type: "postgres", url: "postgres://db/x", max_conns: 10 } });
  wrap();

  fireEvent.change(within(editor("c")).getByLabelText("Type"), { target: { value: "redis" } });
  fireEvent.click(screen.getByRole("option", { name: "redis" }));

  expect(useGraphStore.getState().connections).toEqual({ c: { type: "redis" } });
});

test("removing a connection moves the editor to what is left", () => {
  setConnections({ a: { type: "aws" }, b: { type: "postgres" } });
  wrap();

  fireEvent.click(within(editor("a")).getByRole("button", { name: "Remove" }));

  expect(Object.keys(useGraphStore.getState().connections)).toEqual(["b"]);
  expect(editor("b")).toBeInTheDocument();
});
