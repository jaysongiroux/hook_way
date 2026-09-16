import { expect, test, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { ImportModal } from "./ImportModal";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";

beforeEach(() => useGraphStore.setState({ ...emptyDocument(), selectedId: null, selectedEdgeId: null }));

test("importing flow YAML populates the store graph", () => {
  render(
    <MantineProvider theme={theme}>
      <ImportModal opened onClose={() => {}} />
    </MantineProvider>,
  );
  const yaml = `server: { addr: ":8080", max_body_bytes: 1048576 }
destinations:
  d: { type: webhook, webhook: { url: https://example.test } }
flows:
  a:
    nodes:
      - {id: in, type: http_trigger, path: /a, methods: [POST]}
      - {id: out, type: publish, destination: d, target: t}
    edges: [in>out]
`;
  fireEvent.change(screen.getByLabelText("Config YAML"), { target: { value: yaml } });
  fireEvent.click(screen.getByText("Import"));
  expect(useGraphStore.getState().nodes.map((n) => n.id).sort()).toEqual(["in", "out"]);
  expect(useGraphStore.getState().activeFlow).toBe("a");
});
