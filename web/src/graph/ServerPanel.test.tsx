import { beforeEach, expect, test } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ServerPanel } from "./ServerPanel";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";
import { fromConfig } from "./fromConfig";

beforeEach(() => {
  useGraphStore.setState({
    serverConfig: { addr: ":8080", max_body_bytes: 1048576 },
  });
});

function wrap() {
  return render(
    <MantineProvider theme={theme}>
      <ServerPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
}

test("renders the current server config and edits addr into the store", () => {
  wrap();
  const addr = screen.getByLabelText("Address") as HTMLInputElement;
  expect(addr.value).toBe(":8080");
  fireEvent.change(addr, { target: { value: ":9090" } });
  expect(useGraphStore.getState().serverConfig.addr).toBe(":9090");
});

test("editing a duration field updates the store", () => {
  wrap();
  fireEvent.change(screen.getByLabelText("Read timeout"), {
    target: { value: "5s" },
  });
  expect(useGraphStore.getState().serverConfig.read_timeout).toBe("5s");
});

test("clearing a field removes it from the server config (no empty export)", () => {
  useGraphStore.setState({
    serverConfig: {
      addr: ":8080",
      max_body_bytes: 1048576,
      read_timeout: "5s",
    },
  });
  wrap();
  fireEvent.change(screen.getByLabelText("Read timeout"), {
    target: { value: "" },
  });
  expect(useGraphStore.getState().serverConfig).not.toHaveProperty(
    "read_timeout",
  );
});

test("shutdown budgets survive export and import without dropping other server settings", () => {
  useGraphStore.getState().clear();
  useGraphStore.setState({
    serverConfig: {
      addr: ":8080",
      max_body_bytes: 1048576,
      readiness_require_all: true,
    },
  });
  wrap();
  fireEvent.change(screen.getByLabelText("Traffic withdrawal delay"), {
    target: { value: "5s" },
  });
  fireEvent.change(screen.getByLabelText("Total shutdown limit"), {
    target: { value: "90s" },
  });
  const config = toConfig(useGraphStore.getState().document()).config;
  const imported = fromConfig(config);
  expect(imported.serverConfig).toMatchObject({
    shutdown_delay: "5s",
    shutdown_total_timeout: "90s",
    readiness_require_all: true,
  });
});
