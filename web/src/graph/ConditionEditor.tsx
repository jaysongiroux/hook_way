import { useRef } from "react";
import { useFieldDraft } from "./formDrafts";
import {
  Alert,
  Button,
  Group,
  NativeSelect,
  NumberInput,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";

const SOURCES = [
  { value: "body", label: "Body field" },
  { value: "header", label: "Request header" },
];
const OPERATORS = [
  ["eq", "Equals"],
  ["ne", "Does not equal"],
  ["gt", "Greater than"],
  ["gte", "Greater than or equal"],
  ["lt", "Less than"],
  ["lte", "Less than or equal"],
  ["contains", "Contains"],
  ["prefix", "Starts with"],
  ["suffix", "Ends with"],
  ["regex", "Matches regular expression"],
  ["exists", "Exists"],
  ["not_exists", "Does not exist"],
].map(([value, label]) => ({ value, label }));
const numeric = new Set(["gt", "gte", "lt", "lte"]);
const existence = new Set(["exists", "not_exists"]);
const newCondition = () => ({
  source: "body",
  path: "",
  operator: "eq",
  value: "",
});
export function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

/** Stable keys belong to rows, not their positions or the text being edited. */
export function useRowKeys(values: unknown[], scope?: string) {
  const [saved, save] = useFieldDraft(scope, "_rowKeys");
  const state = useRef<{
    values: unknown[];
    keys: string[];
    next: number;
    scope?: string;
  }>({ values: [], keys: saved?.rowIds ?? [], next: 0, scope });
  if (state.current.scope !== scope)
    state.current = { values: [], keys: saved?.rowIds ?? [], next: 0, scope };
  const current = state.current;
  if (current.values !== values) {
    const used = new Set<string>();
    const keys = values.map((item, index) => {
      const match = current.values.findIndex(
        (old, i) => old === item && !used.has(current.keys[i]),
      );
      const retained = match >= 0 ? current.keys[match] : current.keys[index];
      let key =
        retained && !used.has(retained) ? retained : `row-${current.next++}`;
      while (used.has(key) || (!retained && current.keys.includes(key)))
        key = `row-${current.next++}`;
      used.add(key);
      return key;
    });
    current.values = values;
    current.keys = keys;
  }
  return {
    keys: current.keys,
    accept: (next: unknown[], keys: string[]) => {
      current.values = next;
      current.keys = keys;
      if (scope) save({ rowIds: [...keys], error: "" });
    },
    create: () => {
      let key = `row-${current.next++}`;
      while (current.keys.includes(key)) key = `row-${current.next++}`;
      return key;
    },
  };
}

function choices(options: { value: string; label: string }[], value: unknown) {
  const str = value === undefined ? "" : String(value);
  return options.some((option) => option.value === str)
    ? options
    : [
        { value: str, label: str ? `Unsupported: ${str}` : "Choose an option" },
        ...options,
      ];
}

export interface ConditionEditorProps {
  value: unknown;
  onChange: (value: unknown) => void;
  scope?: string;
  single?: boolean;
}

export function ConditionEditor({
  value,
  onChange,
  scope,
  single = false,
}: ConditionEditorProps) {
  const list = single ? [value] : Array.isArray(value) ? value : [];
  const rows = useRowKeys(list, scope);
  const changeRow = (index: number, next: unknown) => {
    if (single) return onChange(next);
    const updated = list.map((item, i) => (i === index ? next : item));
    rows.accept(updated, rows.keys);
    onChange(updated);
  };
  return (
    <Stack gap="sm" data-form-scope={scope}>
      {!single && !Array.isArray(value) && value !== undefined && (
        <Alert color="red">
          Imported conditions must be a list. The original value is retained
          until you replace it.
        </Alert>
      )}
      {!single && list.length === 0 && (
        <Text size="sm" c="red">
          Add at least one condition.
        </Text>
      )}
      {list.map((item, index) => (
        <Stack
          key={rows.keys[index]}
          gap="xs"
          role="group"
          aria-label={single ? "Condition" : `Condition ${index + 1}`}
        >
          <ConditionFields
            value={item}
            onChange={(next) => changeRow(index, next)}
            labelPrefix={single ? "" : `Condition ${index + 1} `}
          />
          {!single && (
            <Button
              size="xs"
              variant="subtle"
              color="red"
              onClick={() => {
                const next = list.filter((_, i) => i !== index);
                rows.accept(
                  next,
                  rows.keys.filter((_, i) => i !== index),
                );
                onChange(next);
              }}
            >
              Remove condition {index + 1}
            </Button>
          )}
        </Stack>
      ))}
      {!single && (
        <Button
          variant="light"
          size="xs"
          onClick={() => {
            const next = [...list, newCondition()];
            rows.accept(next, [...rows.keys, rows.create()]);
            onChange(next);
          }}
        >
          {!Array.isArray(value) && value !== undefined
            ? "Replace with a condition list"
            : "Add condition"}
        </Button>
      )}
    </Stack>
  );
}

function ConditionFields({
  value,
  onChange,
  labelPrefix,
}: {
  value: unknown;
  onChange: (next: unknown) => void;
  labelPrefix: string;
}) {
  const condition = asRecord(value);
  if (!condition)
    return (
      <>
        <Alert color="red">
          This imported condition is not an object. Its value is retained until
          you replace it.
        </Alert>
        <Text size="xs" style={{ overflowWrap: "anywhere" }}>
          {JSON.stringify(value)}
        </Text>
        <Button size="xs" onClick={() => onChange(newCondition())}>
          Replace condition
        </Button>
      </>
    );
  const operator = String(condition.operator ?? "");
  const source = String(condition.source ?? "");
  const equals = operator === "eq" || operator === "ne";
  const valueType = typeof condition.value;
  const validScalar = ["string", "number", "boolean"].includes(valueType);
  const type = equals
    ? validScalar
      ? valueType
      : ""
    : numeric.has(operator)
      ? "number"
      : "string";
  const valueError = existence.has(operator)
    ? condition.value !== undefined && condition.value !== null
      ? "This operator forbids a value. Remove it to keep the existence check."
      : undefined
    : !validScalar
      ? "Choose a string, number, or boolean value. The imported value is retained until you edit it."
      : numeric.has(operator) && typeof condition.value !== "number"
        ? "Enter a number for this comparison."
        : !equals &&
            !numeric.has(operator) &&
            typeof condition.value !== "string"
          ? "Enter a string for this operator."
          : undefined;
  const update = (key: string, next: unknown) =>
    onChange({ ...condition, [key]: next });
  return (
    <>
      <NativeSelect
        label={`${labelPrefix}Source`}
        aria-label={`${labelPrefix}Source`}
        data={choices(SOURCES, condition.source)}
        value={source}
        required
        error={
          SOURCES.some((choice) => choice.value === source)
            ? undefined
            : "Choose Body field or Request header."
        }
        onChange={(event) => update("source", event.currentTarget.value)}
      />
      <TextInput
        label={`${labelPrefix}${source === "header" ? "Header name" : "Body path"}`}
        aria-label={`${labelPrefix}${source === "header" ? "Header name" : "Body path"}`}
        value={
          typeof condition.path === "string"
            ? condition.path
            : String(condition.path ?? "")
        }
        required
        error={
          typeof condition.path === "string" && condition.path.trim()
            ? undefined
            : "Enter a header name or a dotted body path."
        }
        description={
          source === "header"
            ? "For example X-Event-Type. Header names are case-insensitive."
            : "For example event.type. XML paths use element names and @attribute; array indexes are not supported."
        }
        onChange={(event) => update("path", event.currentTarget.value)}
      />
      <NativeSelect
        label={`${labelPrefix}Operator`}
        aria-label={`${labelPrefix}Operator`}
        data={choices(OPERATORS, condition.operator)}
        value={operator}
        required
        error={
          OPERATORS.some((choice) => choice.value === operator)
            ? undefined
            : "Choose a supported comparison operator."
        }
        onChange={(event) => {
          const op = event.currentTarget.value;
          const next: Record<string, unknown> = { ...condition, operator: op };
          if (existence.has(op)) delete next.value;
          else if (numeric.has(op) && typeof next.value !== "number")
            next.value = 0;
          else if (!["eq", "ne"].includes(op) && typeof next.value !== "string")
            next.value = "";
          else if (["eq", "ne"].includes(op) && !validScalar) next.value = "";
          onChange(next);
        }}
      />
      {existence.has(operator) ? (
        <Text size="xs">
          Checks whether the field is present. JSON null counts as present.
        </Text>
      ) : (
        <>
          {equals && (
            <NativeSelect
              label={`${labelPrefix}Value type`}
              aria-label={`${labelPrefix}Value type`}
              data={choices(
                [
                  { value: "string", label: "String" },
                  { value: "number", label: "Number" },
                  { value: "boolean", label: "Boolean" },
                ],
                type,
              )}
              value={type}
              onChange={(event) => {
                const nextType = event.currentTarget.value;
                update(
                  "value",
                  nextType === "number"
                    ? 0
                    : nextType === "boolean"
                      ? false
                      : "",
                );
              }}
            />
          )}
          {type === "number" ? (
            <NumberInput
              label={`${labelPrefix}Value`}
              aria-label={`${labelPrefix}Value`}
              value={
                typeof condition.value === "number"
                  ? condition.value
                  : typeof condition.value === "string"
                    ? condition.value
                    : ""
              }
              required
              error={valueError}
              onChange={(next) => update("value", next)}
            />
          ) : type === "boolean" ? (
            <NativeSelect
              label={`${labelPrefix}Value`}
              aria-label={`${labelPrefix}Value`}
              value={String(condition.value)}
              data={[
                { value: "true", label: "True" },
                { value: "false", label: "False" },
              ]}
              onChange={(event) =>
                update("value", event.currentTarget.value === "true")
              }
            />
          ) : (
            <TextInput
              label={`${labelPrefix}Value`}
              aria-label={`${labelPrefix}Value`}
              value={typeof condition.value === "string" ? condition.value : ""}
              required
              error={valueError}
              description={
                operator === "regex"
                  ? "Use a Go RE2 pattern. Lookaround and backreferences are not supported."
                  : "Enter text directly, without JSON quotes."
              }
              onChange={(event) => update("value", event.currentTarget.value)}
            />
          )}
        </>
      )}
      {existence.has(operator) && valueError && (
        <Alert color="red">
          <Stack gap="xs">
            <Text size="sm">{valueError}</Text>
            <Button
              size="xs"
              variant="light"
              color="red"
              onClick={() => {
                const next = { ...condition };
                delete next.value;
                onChange(next);
              }}
            >
              Remove comparison value
            </Button>
          </Stack>
        </Alert>
      )}
      {Object.keys(condition).some(
        (key) => !["source", "path", "operator", "value"].includes(key),
      ) && (
        <Text size="xs" c="red">
          Additional imported condition fields are retained; check them in YAML.
        </Text>
      )}
    </>
  );
}

export function SwitchCasesEditor({
  value,
  onChange,
  scope,
}: Omit<ConditionEditorProps, "single">) {
  const cases: unknown[] = Array.isArray(value) ? value : [];
  const rows = useRowKeys(cases, scope);
  const change = (index: number, next: unknown) => {
    const updated = cases.map((item, i) => (i === index ? next : item));
    rows.accept(updated, rows.keys);
    onChange(updated);
  };
  return (
    <Stack gap="sm" data-form-scope={scope}>
      {!Array.isArray(value) && value !== undefined && (
        <Alert color="red">
          Imported cases must be a list. Replace the list to repair it.
        </Alert>
      )}
      {cases.length === 0 && (
        <Text c="red" size="sm">
          Add at least one case.
        </Text>
      )}
      <Text size="sm">
        Cases are tested in order; the first matching case selects its output
        port. Connect each port to the next node.
      </Text>
      {cases.map((item, index) => {
        const entry = asRecord(item);
        if (!entry)
          return (
            <Stack key={rows.keys[index]}>
              <Alert color="red">
                Case {index + 1} is not an object. The original value is
                retained.
              </Alert>
              <Button
                onClick={() =>
                  change(index, { port: "", when: newCondition() })
                }
              >
                Replace case {index + 1}
              </Button>
            </Stack>
          );
        const isSingle = entry.when !== undefined;
        const port = String(entry.port ?? "");
        const portError = !port
          ? "Name the output port."
          : port === "on_error"
            ? "on_error is reserved."
            : /[\s:>\p{Cc}]/u.test(port)
              ? "Ports cannot contain whitespace, control characters, : or >."
              : cases.some(
                    (other, i) => i !== index && asRecord(other)?.port === port,
                  )
                ? "Each case needs a unique port."
                : undefined;
        return (
          <Stack
            key={rows.keys[index]}
            gap="xs"
            role="group"
            aria-label={`Case ${index + 1}`}
          >
            <TextInput
              label={`Case ${index + 1} output port`}
              aria-label={`Case ${index + 1} output port`}
              value={port}
              required
              error={portError}
              onChange={(event) =>
                change(index, { ...entry, port: event.currentTarget.value })
              }
            />
            {entry.when !== undefined && entry.conditions !== undefined && (
              <Alert color="red">
                This case contains both when and conditions. Choose a condition
                mode to keep only the active form.
              </Alert>
            )}
            {isSingle && entry.match !== undefined && (
              <Alert color="red">
                Match all/any has no effect on a single condition. Choose a
                condition mode to remove it.
              </Alert>
            )}
            {isSingle &&
              (entry.conditions !== undefined || entry.match !== undefined) && (
                <Button
                  size="xs"
                  variant="light"
                  onClick={() => {
                    const next = { ...entry };
                    delete next.conditions;
                    delete next.match;
                    change(index, next);
                  }}
                >
                  Keep single condition for case {index + 1}
                </Button>
              )}
            <NativeSelect
              label={`Case ${index + 1} condition mode`}
              aria-label={`Case ${index + 1} condition mode`}
              value={isSingle ? "single" : "list"}
              data={[
                { value: "single", label: "Single condition" },
                { value: "list", label: "Condition list" },
              ]}
              onChange={(event) => {
                const next = { ...entry };
                if (event.currentTarget.value === "single") {
                  next.when =
                    entry.when ??
                    (Array.isArray(entry.conditions)
                      ? entry.conditions[0]
                      : undefined) ??
                    newCondition();
                  delete next.conditions;
                  delete next.match;
                } else {
                  next.conditions = Array.isArray(entry.conditions)
                    ? entry.conditions
                    : [entry.when ?? newCondition()];
                  next.match = entry.match === "any" ? "any" : "all";
                  delete next.when;
                }
                change(index, next);
              }}
            />
            {!isSingle && (
              <NativeSelect
                label={`Case ${index + 1} match`}
                aria-label={`Case ${index + 1} match`}
                value={String(
                  entry.match === undefined || entry.match === ""
                    ? "all"
                    : entry.match,
                )}
                data={choices(
                  [
                    { value: "all", label: "All conditions" },
                    { value: "any", label: "Any condition" },
                  ],
                  entry.match === undefined || entry.match === ""
                    ? "all"
                    : entry.match,
                )}
                error={
                  entry.match === undefined ||
                  ["all", "any", ""].includes(String(entry.match))
                    ? undefined
                    : "Choose All conditions or Any condition."
                }
                onChange={(event) =>
                  change(index, { ...entry, match: event.currentTarget.value })
                }
              />
            )}
            <ConditionEditor
              value={isSingle ? entry.when : entry.conditions}
              single={isSingle}
              scope={`${scope ?? "switch"}.${rows.keys[index]}`}
              onChange={(next) =>
                change(index, {
                  ...entry,
                  [isSingle ? "when" : "conditions"]: next,
                })
              }
            />
            <Group gap="xs">
              <Button
                variant="subtle"
                size="xs"
                disabled={index === 0}
                onClick={() => {
                  const next = [...cases];
                  const keys = [...rows.keys];
                  [next[index - 1], next[index]] = [
                    next[index],
                    next[index - 1],
                  ];
                  [keys[index - 1], keys[index]] = [
                    keys[index],
                    keys[index - 1],
                  ];
                  rows.accept(next, keys);
                  onChange(next);
                }}
              >
                Move case {index + 1} earlier
              </Button>
              <Button
                variant="subtle"
                size="xs"
                disabled={index === cases.length - 1}
                onClick={() => {
                  const next = [...cases];
                  const keys = [...rows.keys];
                  [next[index + 1], next[index]] = [
                    next[index],
                    next[index + 1],
                  ];
                  [keys[index + 1], keys[index]] = [
                    keys[index],
                    keys[index + 1],
                  ];
                  rows.accept(next, keys);
                  onChange(next);
                }}
              >
                Move case {index + 1} later
              </Button>
              <Button
                variant="subtle"
                color="red"
                size="xs"
                onClick={() => {
                  const next = cases.filter((_, i) => i !== index);
                  rows.accept(
                    next,
                    rows.keys.filter((_, i) => i !== index),
                  );
                  onChange(next);
                }}
              >
                Remove case {index + 1}
              </Button>
            </Group>
          </Stack>
        );
      })}
      <Group>
        <Button
          variant="light"
          size="xs"
          onClick={() => {
            const next = [...cases, { port: "", when: newCondition() }];
            rows.accept(next, [...rows.keys, rows.create()]);
            onChange(next);
          }}
        >
          {!Array.isArray(value) && value !== undefined
            ? "Replace with a case list"
            : "Add case"}
        </Button>
      </Group>
    </Stack>
  );
}
