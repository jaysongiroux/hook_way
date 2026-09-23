import { useState } from "react";
import { Button, Divider, Group, Select, Stack, Text, TextInput } from "@mantine/core";
import { CONN_TYPES, connSchema } from "./nodeSchema";
import { destFieldHelp } from "./help";
import { SchemaForm } from "./SchemaForm";
import { destinationPresentation } from "../marketing/destinationIdentity";
import { DARK } from "../tokens";

export interface ConnectionEditorProps {
  name: string;
  entry: Record<string, unknown>;
  /** Names already in use, including this one. */
  taken: string[];
  onRename: (next: string) => void;
  onChange: (entry: Record<string, unknown>) => void;
  onRemove: () => void;
}

/**
 * One connection, full width. The caller keys this on the connection name so
 * the name draft below resets by remount when the selection moves.
 */
export function ConnectionEditor({ name, entry, taken, onRename, onChange, onRemove }: ConnectionEditorProps) {
  const type = String(entry.type ?? CONN_TYPES[0]);
  const schema = connSchema(type) ?? connSchema(CONN_TYPES[0]);
  const { label, icon: Icon } = destinationPresentation(type);

  // A connection is flat, so its own fields live alongside `type` rather than
  // under a block. Splitting them out keeps SchemaForm from rendering `type`
  // as an editable field and duplicating the Select below.
  const { type: _omitted, ...fields } = entry;

  const changeType = (next: string) => {
    // Nothing carries over between connection types: field names overlap (url,
    // tls) but their meaning and legal values do not, and a stale key exports
    // as config the validator rejects for the new type.
    onChange({ type: next });
  };

  return (
    <Stack gap="sm" data-testid={`connection-${name}`}>
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
          description="Backend family this credential reaches."
          data={CONN_TYPES}
          value={type}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => changeType(v ?? type)}
        />
      </Group>
      <SchemaForm
        schema={schema}
        value={fields}
        onChange={(next) => onChange({ type, ...(next as Record<string, unknown>) })}
        fieldHelp={destFieldHelp}
        groupObjects
      />
    </Stack>
  );
}

// Renaming commits on blur rather than per keystroke: connections are held in
// a plain object keyed by name, so an eager rename deletes and re-adds the key
// on every character, walking the row down the rail while you type in it.
function NameField({ name, taken, onRename }: { name: string; taken: string[]; onRename: (next: string) => void }) {
  const [draft, setDraft] = useState(name);
  const [error, setError] = useState("");

  const collides = (value: string) => value !== name && taken.includes(value);
  // Stricter than a destination id, which also allows dots. Issue field paths
  // are resolved segment by segment, so a dot in a connection name makes them
  // ambiguous; internal/config rejects it too.
  const malformed = (value: string) => value !== "" && !/^[a-zA-Z0-9_-]{1,128}$/.test(value);

  return (
    <TextInput
      label="Name"
      description="Id used in a destination's connection field."
      value={draft}
      error={error || undefined}
      onChange={(e) => {
        const next = e.currentTarget.value;
        setDraft(next);
        if (collides(next.trim())) setError("That connection already exists.");
        else if (malformed(next.trim())) setError("Use letters, numbers, underscore, or hyphen only.");
        else setError("");
      }}
      onBlur={() => {
        const next = draft.trim();
        if (!next || next === name || collides(next) || malformed(next)) {
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
