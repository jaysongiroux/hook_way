import { expect, test, beforeEach, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { DestinationsPanel } from "./DestinationsPanel";
import { useGraphStore } from "./store";

function wrap(onClose = vi.fn()) {
  return render(
    <MantineProvider theme={theme}>
      <DestinationsPanel opened onClose={onClose} />
    </MantineProvider>,
  );
}

function setDestinations(destinations: Record<string, unknown>) {
  useGraphStore.setState({ destinations });
}

function rail() {
  return screen.getByRole("navigation", { name: "Destinations" });
}

function editor(name: string) {
  return screen.getByTestId(`destination-${name}`);
}

beforeEach(() => {
  useGraphStore.getState().clear();
});

test("lists every destination in the rail and edits the first one", () => {
  setDestinations({
    orders: { type: "sqs", sqs: { region: "us-east-1" } },
    archive: { type: "s3", s3: { bucket: "cold" } },
  });
  wrap();

  expect(within(rail()).getByRole("button", { name: "Select orders" })).toBeInTheDocument();
  expect(within(rail()).getByRole("button", { name: "Select archive" })).toBeInTheDocument();
  expect(within(editor("orders")).getByLabelText("region")).toHaveValue("us-east-1");
  expect(screen.queryByTestId("destination-archive")).not.toBeInTheDocument();
});

test("picking a rail row swaps which destination the editor holds", () => {
  setDestinations({ orders: { type: "sqs", sqs: {} }, archive: { type: "s3", s3: { bucket: "cold" } } });
  wrap();

  fireEvent.click(within(rail()).getByRole("button", { name: "Select archive" }));

  expect(within(editor("archive")).getByLabelText("bucket")).toHaveValue("cold");
});

test("an empty config opens on the type grid instead of a blank pane", () => {
  wrap();
  expect(screen.getByRole("button", { name: "Webhook" })).toBeInTheDocument();
});

test("choosing a type from the grid creates that backend and edits it", () => {
  setDestinations({ orders: { type: "sqs", sqs: {} } });
  wrap();

  fireEvent.click(screen.getByRole("button", { name: "Add destination" }));
  fireEvent.click(screen.getByRole("button", { name: "Amazon S3" }));

  expect(useGraphStore.getState().destinations).toEqual({
    orders: { type: "sqs", sqs: {} },
    s3: { type: "s3", s3: {} },
  });
  expect(editor("s3")).toBeInTheDocument();
});

test("a second destination of the same type gets a suffixed name", () => {
  setDestinations({ webhook: { type: "webhook", webhook: {} } });
  wrap();

  fireEvent.click(screen.getByRole("button", { name: "Add destination" }));
  fireEvent.click(screen.getByRole("button", { name: "Webhook" }));

  expect(Object.keys(useGraphStore.getState().destinations)).toEqual(["webhook", "webhook_2"]);
});

test("renaming commits on blur and leaves the destination in place", () => {
  setDestinations({
    a: { type: "sqs", sqs: {} },
    b: { type: "s3", s3: { bucket: "cold" } },
    c: { type: "webhook", webhook: {} },
  });
  wrap();
  fireEvent.click(within(rail()).getByRole("button", { name: "Select b" }));

  const name = within(editor("b")).getByLabelText("Name");
  fireEvent.change(name, { target: { value: "archive" } });
  expect(Object.keys(useGraphStore.getState().destinations)).toEqual(["a", "b", "c"]);

  fireEvent.blur(name);

  expect(Object.keys(useGraphStore.getState().destinations)).toEqual(["a", "archive", "c"]);
  expect(useGraphStore.getState().destinations.archive).toEqual({ type: "s3", s3: { bucket: "cold" } });
  expect(within(editor("archive")).getByLabelText("Name")).toHaveValue("archive");
});

test("renaming onto a name already in use is refused", () => {
  setDestinations({ a: { type: "sqs", sqs: {} }, b: { type: "s3", s3: {} } });
  wrap();

  const name = within(editor("a")).getByLabelText("Name");
  fireEvent.change(name, { target: { value: "b" } });
  fireEvent.blur(name);

  expect(screen.getByText("That destination already exists.")).toBeInTheDocument();
  expect(Object.keys(useGraphStore.getState().destinations)).toEqual(["a", "b"]);
});

test("removing the edited destination falls back to the one beside it", () => {
  setDestinations({ a: { type: "sqs", sqs: {} }, b: { type: "s3", s3: {} } });
  wrap();
  fireEvent.click(within(rail()).getByRole("button", { name: "Select b" }));

  fireEvent.click(within(editor("b")).getByRole("button", { name: "Remove" }));

  expect(useGraphStore.getState().destinations).toEqual({ a: { type: "sqs", sqs: {} } });
  expect(editor("a")).toBeInTheDocument();
});

test("nested blocks stay folded so the fields that matter read first", () => {
  setDestinations({ hook: { type: "webhook", webhook: { url: "https://x", retry: { max_attempts: 3 } } } });
  wrap();

  expect(within(editor("hook")).getByLabelText("url")).toHaveValue("https://x");
  expect(screen.queryByLabelText("max attempts")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /^retry/i })).toHaveTextContent("1 set");
});

function chooseType(within_: ReturnType<typeof within>, option: string) {
  fireEvent.click(within_.getByLabelText("Type"));
  fireEvent.click(screen.getByRole("option", { name: option }));
}

test("switching to an unrelated backend starts its block empty", () => {
  setDestinations({ hook: { type: "webhook", webhook: { url: "https://x", timeout: "5s" } } });
  wrap();

  chooseType(within(editor("hook")), "s3");

  expect(useGraphStore.getState().destinations.hook).toEqual({ type: "s3", s3: {} });
});

test("switching between redis variants keeps the connection they share", () => {
  setDestinations({ cache: { type: "redis_kv", redis: { url: "redis://localhost:6379", db: 2 } } });
  wrap();

  chooseType(within(editor("cache")), "redis_list");

  expect(useGraphStore.getState().destinations.cache).toEqual({
    type: "redis_list",
    redis: { url: "redis://localhost:6379", db: 2 },
  });
});
