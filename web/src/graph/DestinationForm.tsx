import { Fieldset, NumberInput, Select, Stack, Switch, TextInput } from "@mantine/core";
import type { DestinationData } from "./model";
import type { FieldSpec } from "./destTypes";
import { DEST_TYPES, DEST_TYPE_SPECS, defaultSettings } from "./destTypes";

// renderField renders one destination setting control by kind, recursing on
// "group" fields into a labeled Fieldset. onChange receives the field's new
// value; a group's onChange receives the merged nested object.
export function renderField(f: FieldSpec, val: unknown, onChange: (v: unknown) => void) {
  if (f.kind === "group") {
    const groupVal = (val ?? {}) as Record<string, unknown>;
    return (
      <Fieldset key={f.key} legend={f.label} radius="sm" p="xs">
        <Stack gap="xs">
          {(f.fields ?? []).map((child) =>
            renderField(child, groupVal[child.key], (cv) => onChange({ ...groupVal, [child.key]: cv })),
          )}
        </Stack>
      </Fieldset>
    );
  }
  if (f.kind === "select") {
    return (
      <Select
        key={f.key}
        label={f.label}
        data={f.options ?? []}
        value={String(val ?? "")}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => onChange(v ?? "")}
      />
    );
  }
  if (f.kind === "bool") {
    return <Switch key={f.key} label={f.label} checked={Boolean(val)} onChange={(e) => onChange(e.currentTarget.checked)} />;
  }
  if (f.kind === "number") {
    return (
      <NumberInput
        key={f.key}
        label={f.label}
        value={val === "" || val == null ? "" : Number(val)}
        onChange={(v) => onChange(v)}
        min={0}
      />
    );
  }
  return (
    <TextInput
      key={f.key}
      label={f.label}
      placeholder={f.placeholder}
      value={String(val ?? "")}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  );
}

// DestinationSettingsFields edits the destination's type and the settings that
// type declares.
export function DestinationSettingsFields({
  data,
  set,
}: {
  data: DestinationData;
  set: (patch: Record<string, unknown>) => void;
}) {
  const spec = DEST_TYPE_SPECS[data.destType];
  const settings = data.settings ?? {};
  const setField = (key: string, value: unknown) => set({ settings: { ...settings, [key]: value } });
  return (
    <Stack gap="xs">
      <Select
        label="Type"
        data={DEST_TYPES}
        value={data.destType}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => {
          const t = v ?? "rabbitmq";
          set({ destType: t, settings: defaultSettings(t) });
        }}
      />
      {spec?.fields.map((f) => renderField(f, settings[f.key], (v) => setField(f.key, v)))}
    </Stack>
  );
}
