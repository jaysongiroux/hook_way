import { Alert, Button, Stack } from "@mantine/core";

export function InactiveSettings({
  value,
  fields,
  onRemove,
}: {
  value: Record<string, unknown>;
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
