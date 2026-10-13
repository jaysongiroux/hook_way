import { useState } from "react";
import { expect, test, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { ConditionEditor, SwitchCasesEditor } from "./ConditionEditor";

function setup(
  value: unknown,
  options: { single?: boolean; cases?: boolean } = {},
) {
  const onChange = vi.fn();
  function Controlled() {
    const [current, setCurrent] = useState(value);
    const change = (next: unknown) => {
      setCurrent(next);
      onChange(next);
    };
    return (
      <MantineProvider>
        {options.cases ? (
          <SwitchCasesEditor value={current} onChange={change} scope="test" />
        ) : (
          <ConditionEditor
            value={current}
            onChange={change}
            single={options.single}
            scope="test"
          />
        )}
      </MantineProvider>
    );
  }
  render(<Controlled />);
  return onChange;
}

const condition = {
  source: "body",
  path: "event.type",
  operator: "eq",
  value: "created",
};

test("ordinary comparisons use supported selectors and unquoted typed values", () => {
  const change = setup(condition, { single: true });
  expect(
    screen.getByLabelText("Source").querySelectorAll("option"),
  ).toHaveLength(3);
  expect(
    screen.getByLabelText("Operator").querySelectorAll("option"),
  ).toHaveLength(12);
  fireEvent.change(screen.getByLabelText("Value"), {
    target: { value: "updated" },
  });
  expect(change).toHaveBeenLastCalledWith({ ...condition, value: "updated" });
  fireEvent.change(screen.getByLabelText("Value type"), {
    target: { value: "number" },
  });
  fireEvent.change(screen.getByLabelText("Value"), {
    target: { value: "12.5" },
  });
  expect(change).toHaveBeenLastCalledWith({ ...condition, value: 12.5 });
  fireEvent.change(screen.getByLabelText("Value type"), {
    target: { value: "boolean" },
  });
  fireEvent.change(screen.getByLabelText("Value"), {
    target: { value: "true" },
  });
  expect(change).toHaveBeenLastCalledWith({ ...condition, value: true });
});

test("numeric, string, regex, and existence operators switch the value contract", () => {
  const change = setup(condition, { single: true });
  fireEvent.change(screen.getByLabelText("Operator"), {
    target: { value: "gte" },
  });
  expect(change).toHaveBeenLastCalledWith({
    ...condition,
    operator: "gte",
    value: 0,
  });
  expect(screen.queryByLabelText("Value type")).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Operator"), {
    target: { value: "regex" },
  });
  fireEvent.change(screen.getByLabelText("Value"), {
    target: { value: "^created$" },
  });
  expect(change).toHaveBeenLastCalledWith({
    ...condition,
    operator: "regex",
    value: "^created$",
  });
  expect(screen.getByText(/Use a Go RE2 pattern/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Operator"), {
    target: { value: "not_exists" },
  });
  expect(change).toHaveBeenLastCalledWith({
    source: "body",
    path: "event.type",
    operator: "not_exists",
  });
  expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
});

test("unsupported imported source, operator, and structured value remain visible and retained", () => {
  const imported = {
    source: "query",
    path: "x",
    operator: "custom",
    value: { future: true },
    extra: "keep",
  };
  const change = setup(imported, { single: true });
  expect(screen.getByText("Unsupported: query")).toBeInTheDocument();
  expect(screen.getByText("Unsupported: custom")).toBeInTheDocument();
  expect(
    screen.getByText(/imported value is retained until you edit it/),
  ).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Body path"), {
    target: { value: "y" },
  });
  expect(change).toHaveBeenLastCalledWith({ ...imported, path: "y" });
});

test("removing an earlier condition retains the later row DOM identity and value", () => {
  const change = setup([
    condition,
    { ...condition, path: "active", value: true },
  ]);
  const later = screen.getByLabelText("Condition 2 Body path");
  fireEvent.click(screen.getByRole("button", { name: "Remove condition 1" }));
  expect(screen.getByLabelText("Condition 1 Body path")).toBe(later);
  expect(later).toHaveValue("active");
  fireEvent.change(later, { target: { value: "enabled" } });
  expect(change).toHaveBeenLastCalledWith([
    { ...condition, path: "enabled", value: true },
  ]);
});

test("switch mode conversion serializes only when or conditions and applies match only to lists", () => {
  const change = setup(
    [
      {
        port: "created",
        when: condition,
        conditions: [condition],
        match: "any",
        custom: "keep",
      },
    ],
    { cases: true },
  );
  expect(
    screen.getByText(/contains both when and conditions/),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Case 1 condition mode"), {
    target: { value: "list" },
  });
  expect(change).toHaveBeenLastCalledWith([
    { port: "created", conditions: [condition], match: "any", custom: "keep" },
  ]);
  fireEvent.change(screen.getByLabelText("Case 1 match"), {
    target: { value: "all" },
  });
  fireEvent.change(screen.getByLabelText("Case 1 condition mode"), {
    target: { value: "single" },
  });
  expect(change).toHaveBeenLastCalledWith([
    { port: "created", when: condition, custom: "keep" },
  ]);
  expect(screen.queryByLabelText("Case 1 match")).not.toBeInTheDocument();
});

test("case ports show duplicate/reserved errors and each list can be edited independently", () => {
  setup(
    [
      { port: "same", when: condition },
      { port: "same", conditions: [condition], match: "all" },
    ],
    { cases: true },
  );
  expect(screen.getAllByText("Each case needs a unique port.")).toHaveLength(2);
  fireEvent.change(screen.getByLabelText("Case 1 output port"), {
    target: { value: "on_error" },
  });
  expect(screen.getByText("on_error is reserved.")).toBeInTheDocument();
  const second = screen.getByRole("group", { name: "Case 2" });
  fireEvent.click(
    within(second).getByRole("button", { name: "Add condition" }),
  );
  expect(
    within(second).getAllByRole("group", { name: /^Condition/ }),
  ).toHaveLength(2);
});

test("malformed imported conditions require an explicit replacement", () => {
  const change = setup({ malformed: true });
  expect(change).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Replace with a condition list" }),
  );
  expect(change).toHaveBeenLastCalledWith([
    { source: "body", path: "", operator: "eq", value: "" },
  ]);
});

test("case priority can change without replacing the moved row or its port", () => {
  const change = setup(
    [
      { port: "first", when: condition },
      { port: "second", when: condition },
    ],
    { cases: true },
  );
  const secondPort = screen.getByLabelText("Case 2 output port");
  fireEvent.click(screen.getByRole("button", { name: "Move case 2 earlier" }));
  expect(screen.getByLabelText("Case 1 output port")).toBe(secondPort);
  expect(change).toHaveBeenLastCalledWith([
    { port: "second", when: condition },
    { port: "first", when: condition },
  ]);
});

test("imported existence values have a direct repair without toggling the operator", () => {
  const change = setup({ ...condition, operator: "exists" }, { single: true });
  fireEvent.click(
    screen.getByRole("button", { name: "Remove comparison value" }),
  );
  expect(change).toHaveBeenLastCalledWith({
    source: "body",
    path: "event.type",
    operator: "exists",
  });
});

test("imported single-case conflicts can be repaired without converting the active mode", () => {
  const change = setup(
    [
      {
        port: "matched",
        when: condition,
        conditions: [condition],
        match: "all",
      },
    ],
    { cases: true },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Keep single condition for case 1" }),
  );
  expect(change).toHaveBeenLastCalledWith([
    { port: "matched", when: condition },
  ]);
});
