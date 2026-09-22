import { useState } from "react";
import { Button, Divider, Group, Select, Stack, Text, TextInput } from "@mantine/core";
import { DEST_SCHEMAS, DEST_TYPES, destSchema } from "./nodeSchema";
import { destFieldHelp } from "./help";
import { SchemaForm } from "./SchemaForm";
import { destinationPresentation } from "../marketing/destinationIdentity";
import { DARK } from "../tokens";

export interface DestinationEditorProps {
  name: string;
  entry: Record<string, unknown>;
  /** Names already in use, including this one. */
  taken: string[];
  onRename: (next: string) => void;
  onChange: (entry: Record<string, unknown>) => void;
  onRemove: () => void;
}

/**
 * The right pane: one destination, full width. The caller keys this on the
 * destination name, so the name draft below resets by remount when the
 * selection moves rather than needing an effect to chase the prop.
 */
export function DestinationEditor({ name, entry, taken, onRename, onChange, onRemove }: DestinationEditorProps) {
  const type = String(entry.type ?? "webhook");
  const spec = destSchema(type) ?? DEST_SCHEMAS.webhook;
  const block = spec.block;
  const blockValue = (entry[block] ?? {}) as Record<string, unknown>;
  const { label, icon: Icon } = destinationPresentation(type);

  const changeType = (next: string) => {
    const nextSpec = destSchema(next);
    const nextBlock = nextSpec?.block ?? next;
    // Values carry over only when the block is the same one -- the four redis_*
    // variants share `redis:`, so switching between them keeps the connection
    // you already typed. Across unrelated backends the old keys are not merely
    // useless, they export as invalid config under the new block's name.
    onChange({ type: next, [nextBlock]: nextBlock === block ? blockValue : {} });
  };

  return (
    <Stack gap="sm" data-testid={`destination-${name}`}>
      <Group justify="space-between" wrap="nowrap" align="center">
        <Group gap="xs" wrap="nowrap" c={DARK.muted}>
          <Icon aria-hidden="true" focusable="false" size={20} />
          <Text fw={600} size="sm" ff="monospace" c={DARK.text}>
            {name}
          </Text>
          <Text size="xs" c="dimmed">
            {label}
          </Text>
        </Group>
        <Button size="xs" variant="subtle" color="red" onClick={onRemove}>
          Remove
        </Button>
      </Group>
      <Divider />
      <Group grow align="flex-start" wrap="nowrap">
        <NameField name={name} taken={taken} onRename={onRename} />
        <Select
          label="Type"
          description="Backend this destination talks to."
          data={DEST_TYPES}
          value={type}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => changeType(v ?? type)}
        />
      </Group>
      <SchemaForm
        schema={spec.schema}
        value={blockValue}
        onChange={(blockVal) => onChange({ type, [block]: blockVal as Record<string, unknown> })}
        fieldHelp={destFieldHelp}
        groupObjects
      />
    </Stack>
  );
}

// Renaming commits on blur rather than per keystroke. The store holds
// destinations in a plain object keyed by name, so an eager rename deletes and
// re-adds the key on every character -- which walks the row to the bottom of
// the rail while you are still typing in it.
function NameField({ name, taken, onRename }: { name: string; taken: string[]; onRename: (next: string) => void }) {
  const [draft, setDraft] = useState(name);
  const [error, setError] = useState("");

  const collides = (value: string) => value !== name && taken.includes(value);

  return (
    <TextInput
      label="Name"
      description="Id used in publish.destination."
      value={draft}
      error={error || undefined}
      onChange={(e) => {
        const next = e.currentTarget.value;
        setDraft(next);
        setError(collides(next.trim()) ? "That destination already exists." : "");
      }}
      onBlur={() => {
        const next = draft.trim();
        if (!next || next === name || collides(next)) {
          if (!next) {
            setDraft(name);
            setError("");
          }
          return;
        }
        onRename(next);
      }}
    />
  );
}
