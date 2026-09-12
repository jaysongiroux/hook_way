import { Select, Stack, TextInput } from "@mantine/core";
import type { FilterData } from "./model";
import { FILTER_SOURCES, OPERATORS } from "./destTypes";

// FilterConditionFields edits the single predicate a filter node applies.
export function FilterConditionFields({
  data,
  set,
}: {
  data: FilterData;
  set: (patch: Record<string, unknown>) => void;
}) {
  return (
    <Stack gap="xs">
      <Select
        label="Source"
        data={FILTER_SOURCES}
        value={data.source}
        onChange={(v) => set({ source: v ?? "body" })}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
      />
      <TextInput
        label="Path"
        placeholder="e.g. action or headers.X-Event"
        value={data.path}
        onChange={(e) => set({ path: e.currentTarget.value })}
      />
      <Select
        label="Operator"
        data={OPERATORS}
        value={data.operator}
        onChange={(v) => set({ operator: v ?? "eq" })}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
      />
      <TextInput label="Value" value={String(data.value ?? "")} onChange={(e) => set({ value: e.currentTarget.value })} />
    </Stack>
  );
}
