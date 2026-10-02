import { createContext, useContext } from "react";
import { Alert, Button, Select, Stack } from "@mantine/core";
import { NODE_SCHEMAS, schemaProperties, type JSONSchema } from "./nodeSchema";
import { SchemaForm, type SchemaFormProps } from "./SchemaForm";
import { nodeFieldHelp, nodeFieldLabels } from "./help";
import { clearDraftBranch } from "./formDrafts";

export type Options = Record<string, unknown>;
export type WorkspacePanel = "routing" | "credentials" | "destinations";
export const WorkspaceActions = createContext<(panel: WorkspacePanel) => void>(
  () => {},
);
export interface OptionsProps {
  nodeType: string;
  value: Options;
  onChange: (v: Options) => void;
  scope: string;
  nodeId?: string;
}
export const object = (v: unknown): Options =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Options) : {};
function fieldsSchema(
  type: string,
  fields: string[],
  required: string[] = [],
  override: Record<string, JSONSchema> = {},
): JSONSchema {
  const properties = schemaProperties(NODE_SCHEMAS[type]?.schema);
  return {
    type: "object",
    additionalProperties: false,
    properties: Object.fromEntries(
      fields
        .filter((k) => properties[k] || override[k])
        .map((k) => [k, override[k] ?? properties[k]]),
    ),
    required,
  };
}
export function Fields({
  nodeType,
  value,
  onChange,
  scope,
  fields,
  required = [],
  override = {},
  labels,
  ...props
}: OptionsProps &
  Omit<SchemaFormProps, "schema" | "value" | "onChange"> & {
    fields: string[];
    required?: string[];
    override?: Record<string, JSONSchema>;
  }) {
  return (
    <SchemaForm
      schema={fieldsSchema(nodeType, fields, required, override)}
      value={value}
      onChange={(v) => onChange(v as Options)}
      scope={scope}
      fieldHelp={(path) => nodeFieldHelp(nodeType, path)}
      labels={{ ...nodeFieldLabels(nodeType), ...labels }}
      {...props}
    />
  );
}
export function changeMode(
  value: Options,
  remove: string[],
  patch: Options,
  onChange: (v: Options) => void,
  scope: string,
) {
  const next = { ...value };
  for (const key of remove) {
    delete next[key];
    clearDraftBranch(scope, key);
  }
  onChange({ ...next, ...patch });
}
export function InactiveSettings({
  value,
  fields,
  onRemove,
}: {
  value: Options;
  fields: string[];
  onRemove: () => void;
}) {
  const present = fields.filter(
    (k) => value[k] !== undefined && value[k] !== "" && value[k] !== false,
  );
  return present.length ? (
    <Alert color="yellow" title="Imported settings need attention">
      <Stack gap="xs">
        These settings do not apply to this mode: {present.join(", ")}. They are
        retained until you change modes or remove them.
        <Button size="xs" variant="light" onClick={onRemove}>
          Remove inactive settings
        </Button>
      </Stack>
    </Alert>
  ) : null;
}
export function ReferenceSelect({
  label,
  value,
  names,
  onChange,
  required = true,
  panel,
  description,
}: {
  label: string;
  value: unknown;
  names: string[];
  onChange: (v: string) => void;
  required?: boolean;
  panel: WorkspacePanel;
  description?: string;
}) {
  const open = useContext(WorkspaceActions);
  const current = String(value ?? "");
  const missing = !!current && !names.includes(current);
  return (
    <Stack gap={4}>
      <Select
        label={label}
        required={required}
        searchable
        allowDeselect={!required}
        data={[...names, ...(missing ? [current] : [])]}
        value={current || null}
        onChange={(v) => onChange(v ?? "")}
        description={description}
        error={
          missing
            ? "This reference is missing or incompatible."
            : required && !current
              ? "Choose a configured reference."
              : undefined
        }
      />
      <Button variant="subtle" size="compact-xs" onClick={() => open(panel)}>
        Manage {panel}
      </Button>
    </Stack>
  );
}
