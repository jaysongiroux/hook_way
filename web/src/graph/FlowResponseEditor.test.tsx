import { beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen, act } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { FlowResponseEditor } from "./FlowResponseEditor";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";

beforeEach(() => useGraphStore.getState().clear());
function mount() {
  return render(
    <MantineProvider>
      <FlowResponseEditor />
    </MantineProvider>,
  );
}

test("retains invalid edits across flow switches and export until corrected", () => {
  mount();
  const input = screen.getByLabelText(
    "HTTP response policy",
  ) as HTMLTextAreaElement;
  fireEvent.change(input, { target: { value: '{"published":' } });
  expect(input).toHaveAttribute("aria-invalid", "true");
  expect(useGraphStore.getState().flowExtra?.responses).toBe('{"published":');
  act(() => {
    useGraphStore.getState().addFlow("second");
  });
  expect(input.value).toBe("");
  act(() => {
    useGraphStore.getState().setActiveFlow("main");
  });
  expect(input.value).toBe('{"published":');
  const edited = '{ "published": { "status": 200, "body": "ok" } }';
  fireEvent.change(input, { target: { value: edited } });
  expect(input.value).toBe(edited);
  expect(input).not.toHaveAttribute("aria-invalid", "true");
  const flows = toConfig(useGraphStore.getState().document()).config
    .flows as Record<string, Record<string, unknown>>;
  expect(flows.main.responses).toEqual({
    published: { status: 200, body: "ok" },
  });
  expect(flows.second.responses).toBeUndefined();
  fireEvent.change(input, { target: { value: "" } });
  expect(useGraphStore.getState().flowExtra?.responses).toBeUndefined();
});
