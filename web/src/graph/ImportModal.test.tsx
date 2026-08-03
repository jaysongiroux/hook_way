import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ImportModal } from "./ImportModal";
import { useGraphStore } from "./store";

beforeEach(() => useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: null }));

test("importing YAML populates the store graph", () => {
  render(
    <MantineProvider theme={theme}>
      <ImportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  const yaml = "destinations:\n  d: { type: rabbitmq, rabbitmq: { url: u, exchange: e, exchange_type: direct } }\nendpoints:\n  a: { enabled: true, path: /a, auth: { type: no_auth, scheme: no_auth }, destinations: [{ destination: d, target: t, create: true }] }\n";
  fireEvent.change(screen.getByLabelText("Config YAML"), { target: { value: yaml } });
  fireEvent.click(screen.getByText("Import"));
  expect(useGraphStore.getState().nodes.map((n) => n.id).sort()).toEqual(["destination:d", "endpoint:a"]);
});
