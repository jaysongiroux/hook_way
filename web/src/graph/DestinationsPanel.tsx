import { Button, Drawer, Group, Select, Stack, Text, TextInput } from "@mantine/core";
import { DEST_SCHEMAS, DEST_TYPES, destSchema } from "./nodeSchema";
import { destFieldHelp } from "./help";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";

export function DestinationsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const destinations = useGraphStore((s) => s.destinations);
  const setDestinations = useGraphStore((s) => s.setDestinations);
  const names = Object.keys(destinations);

  const update = (name: string, entry: Record<string, unknown> | undefined) => {
    const next = { ...destinations };
    if (!entry) delete next[name];
    else next[name] = entry;
    setDestinations(next);
  };

  const add = () => {
    let i = 1;
    let name = "primary";
    while (destinations[name]) {
      i += 1;
      name = `dest_${i}`;
    }
    update(name, { type: "webhook", webhook: {} });
  };

  return (
    <Drawer opened={opened} onClose={onClose} position="right" title="Destinations" size="md">
      <Stack gap="md">
        <Text size="sm" c="dimmed">
          Shared targets referenced by <Text span ff="monospace">publish</Text> nodes. This panel generates config; it
          does not talk to a running Hookway process.
        </Text>
        {names.map((name) => {
          const entry = (destinations[name] ?? {}) as Record<string, unknown>;
          const type = String(entry.type ?? "webhook");
          const spec = destSchema(type) ?? DEST_SCHEMAS.webhook;
          const block = spec.block;
          const blockValue = (entry[block] ?? {}) as Record<string, unknown>;
          return (
            <Stack key={name} gap="xs" p="xs" style={{ border: "1px solid var(--mantine-color-dark-4)", borderRadius: 8 }}>
              <Group justify="space-between" wrap="nowrap">
                <TextInput
                  label="Name"
                  description="Id used in publish.destination."
                  value={name}
                  onChange={(e) => {
                    const nextName = e.currentTarget.value.trim();
                    if (!nextName || nextName === name) return;
                    const next = { ...destinations };
                    delete next[name];
                    next[nextName] = entry;
                    setDestinations(next);
                  }}
                />
                <Button size="xs" variant="subtle" color="red" onClick={() => update(name, undefined)}>
                  Remove
                </Button>
              </Group>
              <Select
                label="Type"
                description="Backend this destination talks to."
                data={DEST_TYPES}
                value={type}
                allowDeselect={false}
                comboboxProps={{ keepMounted: false }}
                onChange={(v) => {
                  const nextType = v ?? type;
                  const nextSpec = destSchema(nextType);
                  const nextBlock = nextSpec?.block ?? nextType;
                  update(name, { type: nextType, [nextBlock]: blockValue });
                }}
              />
              <SchemaForm
                schema={spec.schema}
                value={blockValue}
                onChange={(blockVal) => update(name, { type, [block]: blockVal as Record<string, unknown> })}
                fieldHelp={destFieldHelp}
              />
            </Stack>
          );
        })}
        <Button variant="light" onClick={add}>
          Add destination
        </Button>
      </Stack>
    </Drawer>
  );
}
