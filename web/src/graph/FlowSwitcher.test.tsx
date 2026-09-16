import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { FlowSwitcher } from "./FlowSwitcher";
import { useGraphStore } from "./store";

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <FlowSwitcher />
    </MantineProvider>,
  );
}

beforeEach(() => {
  useGraphStore.getState().clear();
});

async function openCreate() {
  fireEvent.click(screen.getByRole("button", { name: "Active flow" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "New flow" }));
}

test("creates a flow from the modal and switches to it", async () => {
  wrap();
  await openCreate();
  fireEvent.change(await screen.findByLabelText("New flow name"), { target: { value: "billing" } });
  fireEvent.click(screen.getByRole("button", { name: "Create" }));
  expect(useGraphStore.getState().activeFlow).toBe("billing");
});

test("rejects a duplicate flow name", async () => {
  wrap();
  await openCreate();
  fireEvent.change(await screen.findByLabelText("New flow name"), { target: { value: "main" } });
  fireEvent.click(screen.getByRole("button", { name: "Create" }));
  expect(await screen.findByText(/already exists/i)).toBeInTheDocument();
  expect(useGraphStore.getState().activeFlow).toBe("main");
});
