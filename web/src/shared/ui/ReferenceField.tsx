import { Button, Select, Stack } from "@mantine/core";

export function ReferenceField({
  label,
  value,
  names,
  onChange,
  required = true,
  description,
  onManage,
  manageLabel,
}: {
  label: string;
  value: unknown;
  names: string[];
  onChange: (value: string) => void;
  required?: boolean;
  description?: string;
  onManage: () => void;
  manageLabel: string;
}) {
  const current = String(value ?? "");
  const missing = !!current && !names.includes(current);
  return (
    <Stack gap={4}>
      <Select
        label={label}
        aria-label={label}
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
      <Button variant="subtle" size="compact-xs" onClick={onManage}>
        {manageLabel}
      </Button>
    </Stack>
  );
}
