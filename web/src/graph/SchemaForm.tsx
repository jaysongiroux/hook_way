import { useState } from "react";
import { ActionIcon, Button, Fieldset, Group, NumberInput, Select, Stack, Switch, Text, Textarea, TextInput } from "@mantine/core";
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

export function SchemaForm({
  schema,
  value,
  onChange,
  enums,
  onBlurValidate = true,
  fieldHelp,
}: {
  schema: JSONSchema | undefined;
  value: unknown;
  onChange: (value: unknown) => void;
  enums?: Record<string, string[]>;
  onBlurValidate?: boolean;
  fieldHelp?: (name: string) => string | undefined;
}) {
  if (!isSchemaObject(schema)) {
    return (
      <TextInput
        label="Value"
        value={stringify(value)}
        onChange={(e) => onChange(parseScalar(e.currentTarget.value))}
      />
    );
  }
  const properties = schemaProperties(schema);
  const keys = Object.keys(properties);
  if (keys.length === 0 && schemaType(schema) === "object" && isSchemaObject(schema.additionalProperties)) {
    return <MapField value={asObject(value)} onChange={onChange} />;
  }
  const required = new Set(schemaRequired(schema));
  const obj = asObject(value);
  const oneOfNote = schema.oneOf?.length
    ? `Provide exactly one of: ${schema.oneOf
        .flatMap((alt) => (isSchemaObject(alt) ? (alt.required ?? []) : []))
        .map(humanLabel)
        .join(" or ")}.`
    : undefined;
  return (
    <Stack gap="xs">
      {oneOfNote && (
        <Text size="xs" c="dimmed">
          {oneOfNote}
        </Text>
      )}
      {keys.map((key) => (
        <SchemaField
          key={key}
          name={key}
          schema={properties[key]}
          required={required.has(key)}
          value={obj[key]}
          enumOverride={enums?.[key]}
          onBlurValidate={onBlurValidate}
          fieldHelp={fieldHelp}
          onChange={(v) => onChange({ ...obj, [key]: v })}
        />
      ))}
    </Stack>
  );
}

function SchemaField({
  name,
  schema,
  required,
  value,
  onChange,
  enumOverride,
  onBlurValidate,
  fieldHelp,
}: {
  name: string;
  schema: JSONSchema;
  required: boolean;
  value: unknown;
  onChange: (value: unknown) => void;
  enumOverride?: string[];
  onBlurValidate: boolean;
  fieldHelp?: (name: string) => string | undefined;
}) {
  const [error, setError] = useState("");
  const label = humanLabel(name);
  const kind = schemaType(schema);
  const options = enumOverride ?? schemaEnum(schema);
  const hint = (isSchemaObject(schema) ? schema.description : undefined) || fieldHelp?.(name);
  const nestedHelp = (child: string) => fieldHelp?.(`${name}.${child}`) ?? fieldHelp?.(child);
  const validate = () => {
    if (!onBlurValidate) return;
    if (required && isEmpty(value)) setError("Required");
    else setError("");
  };

  if (kind === "boolean") {
    return (
      <Switch
        label={label}
        aria-label={label}
        description={hint}
        checked={Boolean(value)}
        onChange={(e) => onChange(e.currentTarget.checked)}
      />
    );
  }
  if (kind === "enum" || options) {
    return (
      <Select
        label={label}
        aria-label={label}
        data={options ?? []}
        value={value == null || value === "" ? null : String(value)}
        required={required}
        error={error || undefined}
        allowDeselect={!required}
        comboboxProps={{ keepMounted: false }}
        placeholder={placeholder(schema)}
        description={hint}
        onBlur={validate}
        onChange={(v) => onChange(v ?? "")}
      />
    );
  }
  if (kind === "number" || kind === "integer") {
    return (
      <NumberInput
        label={label}
        aria-label={label}
        value={value === "" || value == null ? "" : Number(value)}
        required={required}
        error={error || undefined}
        min={isSchemaObject(schema) ? schema.minimum : undefined}
        max={isSchemaObject(schema) ? schema.maximum : undefined}
        placeholder={placeholder(schema)}
        description={hint}
        onBlur={validate}
        onChange={(v) => onChange(v)}
      />
    );
  }
  if (kind === "array") {
    const items = isSchemaObject(schema) ? schema.items : undefined;
    if (schemaType(items) === "object" || (isSchemaObject(items) && items?.properties)) {
      const rows = Array.isArray(value) ? value : [];
      return (
        <Fieldset legend={label} radius="sm" p="xs">
          <Stack gap="xs">
            {hint && (
              <Text size="xs" c="dimmed">
                {hint}
              </Text>
            )}
            {rows.map((row, i) => (
              <Group key={i} align="flex-start" wrap="nowrap" gap="xs">
                <div style={{ flex: 1 }}>
                  <SchemaForm schema={items} value={row} onChange={(v) => onChange(rows.map((r, j) => (j === i ? v : r)))} fieldHelp={nestedHelp} />
                </div>
                <ActionIcon aria-label={`Remove ${label} ${i + 1}`} variant="subtle" color="red" onClick={() => onChange(rows.filter((_, j) => j !== i))}>
                  ×
                </ActionIcon>
              </Group>
            ))}
            <Button size="xs" variant="light" onClick={() => onChange([...rows, defaultForSchema(items)])}>
              Add {label.replace(/s$/, "")}
            </Button>
          </Stack>
        </Fieldset>
      );
    }
    return (
      <TextInput
        label={label}
        aria-label={label}
        description={hint ? `${hint} Comma-separated.` : "Comma-separated"}
        value={Array.isArray(value) ? value.map(String).join(", ") : String(value ?? "")}
        required={required}
        error={error || undefined}
        placeholder={placeholder(schema)}
        onBlur={validate}
        onChange={(e) =>
          onChange(
            e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter((s) => s.length > 0),
          )
        }
      />
    );
  }
  if (kind === "object") {
    return (
      <Fieldset legend={label} radius="sm" p="xs">
        {hint && (
          <Text size="xs" c="dimmed" mb="xs">
            {hint}
          </Text>
        )}
        <SchemaForm schema={schema} value={asObject(value)} onChange={onChange} fieldHelp={nestedHelp} />
      </Fieldset>
    );
  }
  if (name === "script") {
    return (
      <Textarea
        label={label}
        aria-label={label}
        value={stringify(value)}
        required={required}
        error={error || undefined}
        placeholder={placeholder(schema)}
        description={hint}
        autosize
        minRows={4}
        onBlur={validate}
        onChange={(e) => onChange(e.currentTarget.value)}
      />
    );
  }
  return (
    <TextInput
      label={label}
      aria-label={label}
      description={hint}
      value={stringify(value)}
      required={required}
      error={error || undefined}
      placeholder={placeholder(schema)}
      onBlur={validate}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  );
}

function MapField({ value, onChange }: { value: Record<string, unknown>; onChange: (v: unknown) => void }) {
  const entries = Object.entries(value);
  const setAt = (i: number, key: string, val: string) => {
    const next: Record<string, unknown> = {};
    entries.forEach(([k, v], j) => {
      if (j === i) next[key] = val;
      else next[k] = v;
    });
    onChange(next);
  };
  return (
    <Stack gap="xs">
      {entries.map(([k, v], i) => (
        <Group key={i} gap="xs" wrap="nowrap">
          <TextInput label={i === 0 ? "Key" : undefined} value={k} onChange={(e) => setAt(i, e.currentTarget.value, String(v ?? ""))} />
          <TextInput label={i === 0 ? "Value" : undefined} value={String(v ?? "")} onChange={(e) => setAt(i, k, e.currentTarget.value)} />
          <ActionIcon aria-label={`Remove ${k}`} variant="subtle" color="red" onClick={() => {
            const next = { ...value };
            delete next[k];
            onChange(next);
          }}>
            ×
          </ActionIcon>
        </Group>
      ))}
      <Button size="xs" variant="light" onClick={() => onChange({ ...value, "": "" })}>
        Add entry
      </Button>
    </Stack>
  );
}

function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function stringify(value: unknown): string {
  if (value == null) return "";
  return typeof value === "string" ? value : String(value);
}

function parseScalar(raw: string): unknown {
  const t = raw.trim();
  if (t === "true") return true;
  if (t === "false") return false;
  if (t !== "" && !Number.isNaN(Number(t))) return Number(t);
  return raw;
}

function isEmpty(value: unknown): boolean {
  if (value == null || value === "") return true;
  if (Array.isArray(value)) return value.length === 0;
  return false;
}

function placeholder(schema: JSONSchema): string | undefined {
  if (!isSchemaObject(schema) || schema.default == null) return undefined;
  return String(schema.default);
}
