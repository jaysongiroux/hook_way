import { useEffect, useRef, useState } from "react";
import {
  ActionIcon,
  Button,
  Fieldset,
  Group,
  NumberInput,
  PasswordInput,
  Select,
  Stack,
  Switch,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import {
  defaultForSchema,
  humanLabel,
  isSchemaObject,
  schemaEnum,
  schemaProperties,
  schemaRequired,
  schemaType,
  type JSONSchema,
} from "./nodeSchema";
import { Sections } from "./InspectorSections";
import { goDurationIssue } from "./goDuration";
import { useClearDraftBranch, useFieldDraft } from "./formDrafts";

export interface SchemaFormProps {
  schema: JSONSchema | undefined;
  value: unknown;
  onChange: (value: unknown) => void;
  enums?: Record<string, string[]>;
  onBlurValidate?: boolean;
  fieldHelp?: (name: string) => string | undefined;
  groupObjects?: boolean;
  hideFields?: string[];
  secretFields?: string[];
  scope?: string;
  path?: string;
  optionalBlocks?: boolean;
  order?: string[];
  labels?: Record<string, string>;
  defaults?: Record<string, unknown>;
  multiline?: string[];
  errors?: Record<string, string>;
}

export function SchemaForm(props: SchemaFormProps) {
  const {
    schema,
    value,
    onChange,
    hideFields,
    groupObjects = false,
    path = "",
    scope,
    order = [],
    optionalBlocks = true,
  } = props;
  if (!isSchemaObject(schema))
    return (
      <JsonField
        label="Value"
        value={value}
        onChange={onChange}
        scope={scope}
        path={path}
      />
    );
  const properties = schemaProperties(schema);
  if (Object.keys(properties).length === 0 && schemaType(schema) === "object") {
    if (schema.additionalProperties === false)
      return (
        <Text size="sm" c="dimmed">
          No options required.
        </Text>
      );
    if (
      isSchemaObject(schema.additionalProperties) &&
      schemaType(schema.additionalProperties) === "string"
    )
      return (
        <MapField
          value={asObject(value)}
          onChange={onChange}
          scope={scope}
          path={path}
        />
      );
    return (
      <JsonField
        label={
          props.labels?.[path] ??
          (path ? humanLabel(path.split(".").at(-1) ?? path) : "Value")
        }
        value={value ?? {}}
        onChange={onChange}
        objectOnly
        scope={scope}
        path={path}
      />
    );
  }
  const hidden = new Set(hideFields);
  const keys = [
    ...new Set([
      ...order.filter((k) => k in properties),
      ...Object.keys(properties),
    ]),
  ].filter((key) => !hidden.has(key));
  const required = new Set(schemaRequired(schema));
  const obj = asObject(value);
  const grouped = groupObjects
    ? keys.filter((key) => schemaType(properties[key]) === "object")
    : [];
  const change = (key: string, next: unknown) => {
    const out = { ...obj };
    if (next === undefined) delete out[key];
    else out[key] = next;
    onChange(out);
  };
  const renderField = (key: string) => (
    <SchemaField
      key={key}
      {...props}
      name={key}
      schema={properties[key]}
      value={obj[key]}
      required={required.has(key)}
      onChange={(v) => change(key, v)}
      optionalBlocks={optionalBlocks}
      path={path}
    />
  );
  return (
    <Stack gap="sm">
      {schema.oneOf?.length ? (
        <Text size="xs" c="dimmed">
          Choose one configuration:{" "}
          {schema.oneOf
            .flatMap((alt) => (isSchemaObject(alt) ? (alt.required ?? []) : []))
            .map(humanLabel)
            .join(" or ")}
          .
        </Text>
      ) : null}
      {keys.filter((k) => !grouped.includes(k)).map(renderField)}
      {grouped.length > 0 && (
        <Sections
          defaultOpen={[]}
          sections={grouped.map((key) => ({
            value: key,
            label:
              props.labels?.[path ? `${path}.${key}` : key] ??
              props.labels?.[key] ??
              humanLabel(key),
            badge: Object.keys(asObject(obj[key])).length
              ? `${Object.keys(asObject(obj[key])).length} set`
              : "not set",
            render: () => renderField(key),
          }))}
        />
      )}
    </Stack>
  );
}

function SchemaField(
  props: SchemaFormProps & {
    name: string;
    required: boolean;
    schema: JSONSchema;
  },
) {
  const clearDraftBranch = useClearDraftBranch();
  const {
    name,
    schema,
    required,
    value,
    onChange,
    fieldHelp,
    scope,
    path = "",
    optionalBlocks = true,
    labels,
    defaults,
    errors,
    secretFields,
    multiline,
    onBlurValidate = true,
  } = props;
  const fieldPath = path ? `${path}.${name}` : name;
  const [touched, setTouched] = useState(false);
  const kind = schemaType(schema);
  const label =
    labels?.[fieldPath] ??
    labels?.[name] ??
    (isSchemaObject(schema) ? schema.title : undefined) ??
    humanLabel(name);
  const hint =
    (isSchemaObject(schema) ? schema.description : undefined) ||
    fieldHelp?.(fieldPath) ||
    fieldHelp?.(name);
  const defaultValue =
    defaults?.[fieldPath] ??
    defaults?.[name] ??
    (isSchemaObject(schema) ? schema.default : undefined);
  const issue =
    errors?.[fieldPath] ??
    ((touched || required || value !== undefined) && onBlurValidate
      ? fieldIssue(schema, value, required, name)
      : "");
  const common = {
    label,
    "aria-label": label,
    "data-field-path": fieldPath,
    description: hint,
    required,
    error: issue || undefined,
    onBlur: () => setTouched(true),
  };
  const childProps = {
    ...props,
    schema,
    value,
    onChange,
    path: fieldPath,
    order: undefined,
    hideFields: undefined,
  };
  if (kind === "object") {
    const enabled = value !== undefined && value !== null;
    const optional = optionalBlocks && !required;
    return (
      <Fieldset legend={`${label}${required ? " *" : ""}`} p="sm">
        <Stack gap="sm">
          {hint && (
            <Text size="xs" c="dimmed">
              {hint}
            </Text>
          )}
          {optional && (
            <Switch
              label={`Use ${label}`}
              checked={enabled}
              onChange={(e) => {
                clearDraftBranch(scope, fieldPath);
                onChange(e.currentTarget.checked ? {} : undefined);
              }}
            />
          )}
          {(enabled || !optional) && (
            <SchemaForm {...childProps} value={asObject(value)} />
          )}
          {issue && (
            <Text size="xs" c="red" role="alert">
              {issue}
            </Text>
          )}
        </Stack>
      </Fieldset>
    );
  }
  if (kind === "any")
    return (
      <JsonField
        label={label}
        hint={hint}
        value={value}
        onChange={onChange}
        scope={scope}
        path={fieldPath}
      />
    );
  if (kind === "array") {
    const items = isSchemaObject(schema) ? schema.items : undefined;
    if (
      schemaType(items) === "object" ||
      (isSchemaObject(items) && items.properties)
    )
      return (
        <ObjectRows
          {...childProps}
          label={label}
          hint={hint}
          items={items}
          required={required}
          issue={issue}
        />
      );
    return (
      <TextInput
        {...common}
        description={
          hint ? `${hint} Comma-separated.` : "Comma-separated values."
        }
        value={
          Array.isArray(value)
            ? value.map(String).join(", ")
            : String(value ?? "")
        }
        onChange={(e) =>
          onChange(
            e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
      />
    );
  }
  if (kind === "boolean") {
    const effective =
      value === undefined ? Boolean(defaultValue) : Boolean(value);
    return (
      <Stack gap={4}>
        <Switch
          label={label}
          aria-label={label}
          data-field-path={fieldPath}
          description={hint}
          checked={effective}
          onChange={(e) => onChange(e.currentTarget.checked)}
          error={issue || undefined}
        />
        {defaultValue !== undefined && (
          <Group gap="xs">
            <Text size="xs" c="dimmed">
              {value === undefined
                ? `Using default: ${effective ? "on" : "off"}.`
                : "Explicit override."}
            </Text>
            {value !== undefined && (
              <Button
                size="compact-xs"
                variant="subtle"
                onClick={() => onChange(undefined)}
              >
                Use default for {label}
              </Button>
            )}
          </Group>
        )}
      </Stack>
    );
  }
  const options =
    props.enums?.[fieldPath] ?? props.enums?.[name] ?? schemaEnum(schema);
  if (kind === "enum" || options) {
    const current = value == null || value === "" ? null : String(value);
    const data = [...(options ?? [])];
    const unsupported = current !== null && !data.includes(current);
    if (unsupported) data.push(current);
    return (
      <Select
        {...common}
        data={data}
        value={current}
        searchable
        allowDeselect={!required}
        comboboxProps={{ keepMounted: false }}
        placeholder={
          defaultValue === undefined
            ? "Select…"
            : `Default: ${String(defaultValue)}`
        }
        error={
          unsupported
            ? `Unsupported value: ${current}. Choose a supported option.`
            : common.error
        }
        onChange={(v) => onChange(v ?? undefined)}
      />
    );
  }
  if (kind === "number" || kind === "integer")
    return (
      <NumberInput
        {...common}
        hideControls
        clampBehavior="none"
        allowDecimal={kind !== "integer"}
        value={value === "" || value == null ? "" : Number(value)}
        min={isSchemaObject(schema) ? schema.minimum : undefined}
        max={isSchemaObject(schema) ? schema.maximum : undefined}
        placeholder={
          defaultValue === undefined
            ? undefined
            : `Default: ${String(defaultValue)}`
        }
        onChange={(v) => onChange(v === "" ? undefined : v)}
      />
    );
  const masked =
    secretFields?.includes(fieldPath) || secretFields?.includes(name);
  const isMultiline =
    name === "script" ||
    multiline?.includes(fieldPath) ||
    multiline?.includes(name);
  if (isMultiline)
    return (
      <Textarea
        {...common}
        value={stringify(value)}
        autosize
        minRows={4}
        maxRows={20}
        onChange={(e) =>
          onChange(
            e.currentTarget.value === "" && !required
              ? undefined
              : e.currentTarget.value,
          )
        }
      />
    );
  if (masked)
    return (
      <PasswordInput
        {...common}
        value={stringify(value)}
        onChange={(e) =>
          onChange(
            e.currentTarget.value === "" && !required
              ? undefined
              : e.currentTarget.value,
          )
        }
      />
    );
  return (
    <TextInput
      {...common}
      value={stringify(value)}
      placeholder={
        defaultValue === undefined
          ? undefined
          : `Default: ${String(defaultValue)}`
      }
      onChange={(e) =>
        onChange(
          e.currentTarget.value === "" && !required
            ? undefined
            : e.currentTarget.value,
        )
      }
    />
  );
}

function ObjectRows(
  props: SchemaFormProps & {
    label: string;
    hint?: string;
    items: JSONSchema | undefined;
    required: boolean;
    issue?: string;
  },
) {
  const clearDraftBranch = useClearDraftBranch();
  const {
    value,
    onChange,
    scope,
    path = "",
    label,
    hint,
    items,
    issue,
  } = props;
  const rows = Array.isArray(value) ? value : [];
  const [draft, save] = useFieldDraft(scope, `${path}._rows`);
  const localIds = useRef<string[]>(draft?.rowIds ?? []);
  while (localIds.current.length < rows.length)
    localIds.current.push(nextRowId());
  useEffect(() => {
    if (
      scope &&
      JSON.stringify(draft?.rowIds) !== JSON.stringify(localIds.current)
    )
      save({ rowIds: [...localIds.current], error: "" });
  }, [scope, rows.length, draft?.rowIds, save]);
  const remove = (index: number) => {
    const id = localIds.current[index];
    clearDraftBranch(scope ? `${scope}/${id}` : undefined);
    localIds.current.splice(index, 1);
    save({ rowIds: [...localIds.current], error: "" });
    onChange(rows.filter((_, i) => i !== index));
  };
  return (
    <Fieldset legend={`${label}${props.required ? " *" : ""}`} p="sm">
      <Stack gap="sm">
        {hint && (
          <Text size="xs" c="dimmed">
            {hint}
          </Text>
        )}
        {rows.map((row, i) => (
          <Stack key={localIds.current[i]} gap="xs">
            <Group justify="space-between">
              <Text size="xs" fw={600}>
                {label} {i + 1}
              </Text>
              <ActionIcon
                size="lg"
                aria-label={`Remove ${label} ${i + 1}`}
                variant="subtle"
                color="red"
                onClick={() => remove(i)}
              >
                ×
              </ActionIcon>
            </Group>
            <SchemaForm
              {...props}
              schema={items}
              value={row}
              scope={scope ? `${scope}/${localIds.current[i]}` : undefined}
              onChange={(v) => onChange(rows.map((r, j) => (j === i ? v : r)))}
            />
          </Stack>
        ))}
        {issue && (
          <Text size="xs" c="red" role="alert">
            {issue}
          </Text>
        )}
        <Button
          size="xs"
          variant="light"
          onClick={() => {
            localIds.current.push(nextRowId());
            save({ rowIds: [...localIds.current], error: "" });
            onChange([...rows, defaultForSchema(items)]);
          }}
        >
          Add {label.replace(/s$/, "")}
        </Button>
      </Stack>
    </Fieldset>
  );
}

function JsonField({
  label,
  hint,
  value,
  onChange,
  objectOnly = false,
  scope,
  path = "",
}: {
  label: string;
  hint?: string;
  value: unknown;
  onChange: (v: unknown) => void;
  objectOnly?: boolean;
  scope?: string;
  path?: string;
}) {
  const serialized = JSON.stringify(value) ?? "null";
  const [saved, save] = useFieldDraft(scope, path);
  const [text, setText] = useState(
    saved?.text ?? JSON.stringify(JSON.parse(serialized), null, 2),
  );
  const [error, setError] = useState(saved?.error ?? "");
  const lastValue = useRef(serialized);
  useEffect(() => {
    if (lastValue.current === serialized) return;
    lastValue.current = serialized;
    if (saved?.error) return;
    setText(JSON.stringify(JSON.parse(serialized), null, 2));
    save(undefined);
  }, [serialized, saved?.error]);
  const commit = (next: string) => {
    setText(next);
    try {
      const parsed: unknown = JSON.parse(next);
      if (
        objectOnly &&
        (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      )
        throw new Error("Enter a JSON object.");
      setError("");
      save({ text: next, error: "" });
      lastValue.current = JSON.stringify(parsed);
      onChange(parsed);
    } catch {
      const message = objectOnly
        ? "Enter a valid JSON object. This draft is retained until corrected or reverted."
        : "Enter valid JSON. This draft is retained until corrected or reverted.";
      setError(message);
      save({ text: next, error: message });
    }
  };
  return (
    <Stack gap={4}>
      <Textarea
        label={label}
        aria-label={label}
        data-field-path={path}
        description={hint ?? "JSON"}
        data-json-editor="true"
        value={text}
        error={error || undefined}
        autosize
        minRows={4}
        maxRows={20}
        onChange={(e) => commit(e.currentTarget.value)}
      />
      {error && (
        <Button
          variant="subtle"
          size="compact-xs"
          onClick={() => {
            save(undefined);
            setError("");
            setText(JSON.stringify(JSON.parse(serialized), null, 2));
          }}
        >
          Revert {label} draft
        </Button>
      )}
    </Stack>
  );
}

function MapField({
  value,
  onChange,
  scope,
  path = "",
}: {
  value: Record<string, unknown>;
  onChange: (v: unknown) => void;
  scope?: string;
  path?: string;
}) {
  const [saved, save] = useFieldDraft(scope, path);
  const [rows, setRows] = useState(
    () =>
      saved?.rows ??
      Object.entries(value).map(([key, v]) => ({
        id: nextRowId(),
        key,
        value: String(v ?? ""),
      })),
  );
  const [error, setError] = useState(saved?.error ?? "");
  const lastSerialized = useRef(JSON.stringify(value));
  const serialized = JSON.stringify(value);
  useEffect(() => {
    if (lastSerialized.current === serialized) return;
    lastSerialized.current = serialized;
    if (saved?.error) return;
    setRows(
      Object.entries(value).map(([key, v]) => ({
        id: nextRowId(),
        key,
        value: String(v ?? ""),
      })),
    );
    save(undefined);
  }, [serialized, saved?.error, value]);
  const commit = (next: typeof rows) => {
    setRows(next);
    const keys = next.map((r) => r.key);
    const issue = keys.some((k) => k.trim() === "")
      ? "Every entry needs a key."
      : new Set(keys).size !== keys.length
        ? "Duplicate keys: give each entry a unique key. Both entries are retained."
        : "";
    setError(issue);
    save({ rows: next, error: issue });
    if (!issue) {
      const out = Object.fromEntries(next.map((r) => [r.key, r.value]));
      lastSerialized.current = JSON.stringify(out);
      onChange(out);
    }
  };
  return (
    <Stack gap="sm">
      {rows.map((row, i) => (
        <Group key={row.id} align="flex-start" wrap="wrap" gap="xs">
          <TextInput
            style={{ flex: "1 1 120px" }}
            label={i === 0 ? "Key" : `Key ${i + 1}`}
            value={row.key}
            error={
              rows.filter((r) => r.key === row.key).length > 1
                ? "Duplicate key"
                : undefined
            }
            onChange={(e) =>
              commit(
                rows.map((r) =>
                  r.id === row.id ? { ...r, key: e.currentTarget.value } : r,
                ),
              )
            }
          />
          <TextInput
            style={{ flex: "1 1 120px" }}
            label={i === 0 ? "Value" : `Value ${i + 1}`}
            value={row.value}
            onChange={(e) =>
              commit(
                rows.map((r) =>
                  r.id === row.id ? { ...r, value: e.currentTarget.value } : r,
                ),
              )
            }
          />
          <ActionIcon
            size="lg"
            aria-label={`Remove ${row.key || `entry ${i + 1}`}`}
            variant="subtle"
            color="red"
            mt={24}
            onClick={() => commit(rows.filter((r) => r.id !== row.id))}
          >
            ×
          </ActionIcon>
        </Group>
      ))}
      {error && (
        <Text role="alert" size="xs" c="red">
          {error}
        </Text>
      )}
      <Button
        size="xs"
        variant="light"
        onClick={() =>
          commit([...rows, { id: nextRowId(), key: "", value: "" }])
        }
      >
        Add entry
      </Button>
      {error && (
        <Button
          size="compact-xs"
          variant="subtle"
          onClick={() => {
            save(undefined);
            setError("");
            setRows(
              Object.entries(value).map(([key, v]) => ({
                id: nextRowId(),
                key,
                value: String(v ?? ""),
              })),
            );
          }}
        >
          Revert entries draft
        </Button>
      )}
    </Stack>
  );
}

let rowSequence = 0;
function nextRowId() {
  return `row-${Date.now()}-${++rowSequence}`;
}
function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
function stringify(value: unknown) {
  return value == null ? "" : typeof value === "string" ? value : String(value);
}
function fieldIssue(
  schema: JSONSchema | undefined,
  value: unknown,
  required: boolean,
  name = "",
): string {
  if (value === undefined || value === null || value === "")
    return required ? "Required to configure this node." : "";
  if (!isSchemaObject(schema)) return "";
  if (schema.type === "string" && typeof value !== "string")
    return "Enter a text value; the imported value has a different type.";
  if (
    (schema.type === "number" || schema.type === "integer") &&
    typeof value !== "number"
  )
    return "Enter a number; the imported value has a different type.";
  if (schema.type === "boolean" && typeof value !== "boolean")
    return "Choose true or false; the imported value has a different type.";
  if (schema.type === "array" && !Array.isArray(value))
    return "Enter a list; the imported value has a different type.";
  if (
    schema.type === "object" &&
    (typeof value !== "object" || Array.isArray(value))
  )
    return "Enter an object; the imported value has a different type.";
  if (
    [
      "ttl",
      "timeout",
      "negative_ttl",
      "timestamp_tolerance",
      "backoff",
      "initial_backoff",
      "max_backoff",
      "total_timeout",
      "refresh",
      "overlap",
    ].includes(name)
  ) {
    const duration = goDurationIssue(value, { allowZero: true });
    if (duration) return duration;
  }
  if (schema.enum && !schema.enum.includes(value))
    return "Choose a supported value. The imported value is retained.";
  if (
    Array.isArray(value) &&
    schema.maxItems !== undefined &&
    value.length > schema.maxItems
  )
    return `Use at most ${schema.maxItems} items.`;
  if (
    Array.isArray(value) &&
    schema.minItems !== undefined &&
    value.length < schema.minItems
  )
    return `Add at least ${schema.minItems} item${schema.minItems === 1 ? "" : "s"}.`;
  if (typeof value === "number") {
    if (schema.type === "integer" && !Number.isInteger(value))
      return "Enter a whole number.";
    if (schema.minimum !== undefined && value < schema.minimum)
      return `Minimum: ${schema.minimum}.`;
    if (schema.maximum !== undefined && value > schema.maximum)
      return `Maximum: ${schema.maximum}.`;
  }
  return "";
}
