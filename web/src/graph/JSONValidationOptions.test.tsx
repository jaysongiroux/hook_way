import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { NodeOptionsForm } from "./NodeOptionsForm";
import { useGraphStore } from "./store";
import { draftKey } from "./formDrafts";
import { validateJSONSample } from "../shared/wasm/jsonSample";

vi.mock("../shared/wasm/jsonSample", () => ({ validateJSONSample: vi.fn() }));
const sampleMock = vi.mocked(validateJSONSample);
const scope = "node/test-flow/validate";

beforeEach(() => {
  useGraphStore.getState().clear();
  sampleMock.mockReset();
});

function show(nodeType = "json_validate", value: Record<string, unknown> = { schema: {} }) {
  const change = vi.fn();
  const view = render(<MantineProvider><NodeOptionsForm
    nodeType={nodeType} nodeId="validate" scope={scope} value={value} onChange={change}
  /></MantineProvider>);
  return { ...view, change };
}

test("dedicated node keeps invalid schema and sample drafts across remounts", () => {
  const view = show();
  expect(screen.getByText(/nonempty JSON Schema object/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("JSON Schema"), { target: { value: '{"minimum":9007199254740993}' } });
  expect(view.change).not.toHaveBeenCalled();
  expect(useGraphStore.getState().formDrafts?.[draftKey(scope, "schema")]).toMatchObject({
    text: '{"minimum":9007199254740993}', error: expect.stringContaining("lose precision"),
  });
  fireEvent.change(screen.getByLabelText("JSON sample"), { target: { value: '{"id":' } });
  view.unmount();
  show();
  expect(screen.getByLabelText("JSON Schema")).toHaveValue('{"minimum":9007199254740993}');
  expect(screen.getByLabelText("JSON sample")).toHaveValue('{"id":');
  expect(screen.getByRole("button", { name: "Validate sample" })).toBeDisabled();
});

test("valid schema and explicit conversion are authored without extra transformations", async () => {
  const view = show();
  fireEvent.change(screen.getByLabelText("JSON Schema"), {
    target: { value: '{"type":"object","required":["id"],"properties":{"id":{"type":"integer","format":"int64"}},"additionalProperties":false}' },
  });
  expect(view.change).toHaveBeenLastCalledWith({
    schema: { type: "object", required: ["id"], properties: { id: { type: "integer", format: "int64" } }, additionalProperties: false },
  });
  view.rerender(<MantineProvider><NodeOptionsForm nodeType="json_validate" nodeId="validate" scope={scope}
    value={view.change.mock.lastCall?.[0]} onChange={view.change} /></MantineProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Add conversion" }));
  expect(view.change.mock.lastCall?.[0]).toMatchObject({ coercions: [{ path: "", type: "string" }] });
  view.rerender(<MantineProvider><NodeOptionsForm nodeType="json_validate" nodeId="validate" scope={scope}
    value={view.change.mock.lastCall?.[0]} onChange={view.change} /></MantineProvider>);
  expect(screen.getByLabelText("Property path 1")).toBeInTheDocument();
});

test("sample tester sends exact raw numbers and displays Go output unchanged", async () => {
  const output = '{"id":9007199254740993}';
  sampleMock.mockResolvedValue({ valid: true, issues: [], output });
  show("json_validate", { schema: { type: "object" } });
  fireEvent.change(screen.getByLabelText("JSON sample"), { target: { value: output } });
  fireEvent.click(screen.getByRole("button", { name: "Validate sample" }));
  await waitFor(() => expect(sampleMock).toHaveBeenCalledWith({ schema: { type: "object" }, coercions: [], sample: output }));
  expect(await screen.findByText("Sample is valid")).toBeInTheDocument();
  expect(screen.getByText(output, { selector: "pre" })).toBeInTheDocument();
});

test("edits while validation is pending discard the stale result", async () => {
  let finish!: (value: { valid: boolean; issues: []; output: string }) => void;
  sampleMock.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  show("json_validate", { schema: { type: "object" } });
  fireEvent.change(screen.getByLabelText("JSON sample"), { target: { value: "{}" } });
  fireEvent.click(screen.getByRole("button", { name: "Validate sample" }));
  await waitFor(() => expect(sampleMock).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByLabelText("JSON sample"), { target: { value: '{"different":true}' } });
  finish({ valid: true, issues: [], output: "{}" });
  await waitFor(() => expect(screen.getByRole("button", { name: "Validate sample" })).toBeEnabled());
  expect(screen.queryByText("Sample is valid")).not.toBeInTheDocument();
});

test("legacy JSON Schema transform uses the same schema and conversion editor", () => {
  show("transform", { kind: "json_schema", json_schema: { type: "object" } });
  expect(screen.getAllByLabelText("JSON Schema")).toHaveLength(1);
  expect(screen.getByRole("button", { name: "Add conversion" })).toBeInTheDocument();
  expect(screen.getByLabelText("JSON sample")).toBeInTheDocument();
});
