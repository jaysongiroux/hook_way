import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { OpenAPIWizard } from "./OpenAPIWizard";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";
const mocks = vi.hoisted(() => ({
  inspect: vi.fn(),
  generate: vi.fn(),
  cancel: vi.fn(),
}));
vi.mock("../shared/wasm/openapi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../shared/wasm/openapi")>()),
  OpenAPIImporter: class {
    inspect = mocks.inspect;
    generate = mocks.generate;
    cancel = mocks.cancel;
  },
}));
const operation = (
  method = "GET",
  path = "/items",
  diagnostics: unknown[] = [],
) => ({
  method,
  path,
  name: `${method.toLowerCase()}_items`,
  description: "Items",
  summary: "Items",
  security: null,
  requires_auth: false,
  security_requirements: [],
  input_schema_json: '{"type":"object"}',
  output_schema_json: '{"type":"object"}',
  diagnostics,
});
const initial = () => useGraphStore.getState().document();
beforeEach(() => {
  HTMLElement.prototype.scrollIntoView = vi.fn();
  useGraphStore.getState().setGraph(emptyDocument());
  vi.clearAllMocks();
  mocks.inspect.mockResolvedValue({
    operations: [
      operation(),
      operation("POST"),
      operation("HEAD", "/items", [
        { severity: "error", location: "paths", message: "Unsupported method" },
      ]),
    ],
    diagnostics: [],
  });
});
afterEach(cleanup);
function mount() {
  const onClose = vi.fn();
  render(
    <MantineProvider theme={theme}>
      <OpenAPIWizard onClose={onClose} />
    </MantineProvider>,
  );
  return onClose;
}
async function inspect() {
  fireEvent.change(screen.getByLabelText("OpenAPI document", { exact: true }), {
    target: { value: "raw: 9007199254740993" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Inspect operations" }));
  await screen.findByRole("checkbox", { name: /^GET \/items(?:\/0)?$/ });
}
test("empty selections, unsupported visibility, collisions and source replacement leave workspace intact", async () => {
  const before = initial();
  mount();
  await inspect();
  expect(
    screen.getByRole("button", { name: "Configure access" }),
  ).toBeDisabled();
  expect(screen.getByRole("checkbox", { name: "HEAD /items" })).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox", { name: "GET /items" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "POST /items" }));
  fireEvent.change(screen.getByLabelText("Tool name for POST /items"), {
    target: { value: "get_items" },
  });
  expect(
    screen.getByRole("button", { name: "Configure access" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.change(screen.getByLabelText("OpenAPI document", { exact: true }), {
    target: { value: "new: source" },
  });
  expect(
    screen.queryByRole("checkbox", { name: "GET /items" }),
  ).not.toBeInTheDocument();
  expect(initial()).toEqual(before);
});
test("caller grants start empty and invalid form drafts never write to the graph", async () => {
  const before = initial();
  mount();
  await inspect();
  fireEvent.click(screen.getByRole("checkbox", { name: "POST /items" }));
  fireEvent.click(screen.getByRole("button", { name: "Configure access" }));
  expect(
    screen.getByRole("checkbox", { name: "Allow mutation for POST /items" }),
  ).not.toBeChecked();
  const keys = screen.getByRole("textbox", { name: /api keys/i });
  fireEvent.change(keys, { target: { value: "{" } });
  expect(
    screen.getByRole("button", { name: "Generate review" }),
  ).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(screen.getByRole("button", { name: "Configure access" }));
  expect(keys).toHaveValue("{");
  expect(useGraphStore.getState().formDrafts).toEqual({});
  expect(initial()).toEqual(before);
});
test("canceling and discarding a populated import preserves the builder", async () => {
  const before = initial();
  const close = mount();
  await inspect();
  fireEvent.click(screen.getByRole("button", { name: "Close OpenAPI wizard" }));
  expect(close).not.toHaveBeenCalled();
  fireEvent.click(await screen.findByRole("button", { name: "Keep editing" }));
  await waitFor(() =>
    expect(screen.getByRole("checkbox", { name: "GET /items" })).toBeVisible(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Close OpenAPI wizard" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Discard import" }),
  );
  expect(close).toHaveBeenCalledOnce();
  expect(initial()).toEqual(before);
});

test("selection stops at 64 supported operations", async () => {
  mocks.inspect.mockResolvedValue({
    operations: Array.from({ length: 65 }, (_, i) => ({
      ...operation("GET", `/items/${i}`),
      name: `tool_${i}`,
    })),
    diagnostics: [],
  });
  mount();
  await inspect();
  const boxes = screen.getAllByRole("checkbox");
  act(() => {
    for (const box of boxes.slice(0, 64)) fireEvent.click(box);
  });
  expect(boxes[64]).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Configure access" }),
  ).toBeEnabled();
}, 30000);

test("late inspection results cannot replace a newer source", async () => {
  let resolve!: (value: unknown) => void;
  mocks.inspect.mockReturnValueOnce(
    new Promise((value) => {
      resolve = value;
    }),
  );
  mount();
  fireEvent.change(screen.getByLabelText("OpenAPI document", { exact: true }), {
    target: { value: "old: source" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Inspect operations" }));
  fireEvent.change(screen.getByLabelText("OpenAPI document", { exact: true }), {
    target: { value: "new: source" },
  });
  await act(async () =>
    resolve({ operations: [operation()], diagnostics: [] }),
  );
  expect(
    screen.queryByRole("checkbox", { name: "GET /items" }),
  ).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Inspect operations" }),
  ).toBeEnabled();
});

test("precision failures preserve the workspace and retain the generated download", async () => {
  const before = initial();
  mocks.generate.mockResolvedValue({
    yaml: "flows: {main: {nodes: [{id: mcp, type: mcp_trigger, tools: [{input_schema: {type: integer, maximum: 9007199254740993}}]}], edges: []}}",
    diagnostics: [],
  });
  mount();
  await inspect();
  fireEvent.click(screen.getByRole("checkbox", { name: "GET /items" }));
  fireEvent.click(screen.getByRole("button", { name: "Configure access" }));
  fireEvent.click(screen.getByRole("button", { name: "Generate review" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Open in builder" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Replace workspace" }),
  );
  expect(await screen.findByText(/Your workspace is unchanged/)).toBeVisible();
  expect(initial()).toEqual(before);
  expect(
    screen.getByRole("button", { name: "Download core.yaml" }),
  ).toBeEnabled();
});

test("review diagnostics return focus to the affected endpoint setting", async () => {
  mocks.generate.mockResolvedValue({
    diagnostics: [
      {
        severity: "error",
        location: "endpoint.public_url",
        message: "A public URL is required",
      },
    ],
  });
  mount();
  await inspect();
  fireEvent.click(screen.getByRole("checkbox", { name: "GET /items" }));
  fireEvent.click(screen.getByRole("button", { name: "Configure access" }));
  fireEvent.click(screen.getByRole("button", { name: "Generate review" }));
  fireEvent.click(await screen.findByRole("button", { name: "Review field" }));
  await waitFor(() =>
    expect(
      screen.getByLabelText("Public MCP URL", { exact: true }),
    ).toHaveFocus(),
  );
});
