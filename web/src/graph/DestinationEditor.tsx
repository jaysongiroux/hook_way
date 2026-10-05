import { useState } from "react";
import {
  Button,
  Divider,
  Group,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import {
  DEST_SCHEMAS,
  DEST_TYPES,
  connOwnedPaths,
  connTypeForDest,
  destSchema,
} from "./nodeSchema";
import { destFieldHelp } from "./help";
import { clearDraftBranch } from "./formDrafts";
import { SchemaForm } from "./SchemaForm";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { useGraphStore } from "./store";
import { DARK } from "../tokens";
import { WebhookDeliveryNotice } from "./WebhookDeliveryNotice";

/** The Connection select's "no connection" option. */
const INLINE = "__inline";

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
export function DestinationEditor({
  name,
  entry,
  taken,
  onRename,
  onChange,
  onRemove,
}: DestinationEditorProps) {
  const type = String(entry.type ?? "webhook");
  const spec = destSchema(type) ?? DEST_SCHEMAS.webhook;
  const block = spec.block;
  const blockValue = (entry[block] ?? {}) as Record<string, unknown>;
  const { label, icon: Icon } = destinationPresentation(type);

  const connections = useGraphStore((s) => s.connections);
  const wantConnType = connTypeForDest(type);
  const connection = String(entry.connection ?? "");
  // Only connections whose type can serve this backend are offered. A postgres
  // credential cannot fill an s3 destination, and the Go validator rejects it.
  const usable = Object.entries(connections)
    .filter(
      ([, v]) =>
        (v as Record<string, unknown> | undefined)?.type === wantConnType,
    )
    .map(([name]) => name);
  // Fields the chosen connection supplies are hidden rather than shown empty:
  // setting them here as well is a validation error, so offering them would
  // only let you author config that cannot load.
  const ownedByConnection = connection
    ? connOwnedPaths(wantConnType ?? "")
    : [];

  const changeType = (next: string) => {
    clearDraftBranch(`destination/${name}`);
    const nextSpec = destSchema(next);
    const nextBlock = nextSpec?.block ?? next;
    // Values carry over only when the block is the same one -- the four redis_*
    // variants share `redis:`, so switching between them keeps the connection
    // you already typed. Across unrelated backends the old keys are not merely
    // useless, they export as invalid config under the new block's name.
    const carried = nextBlock === block ? blockValue : {};
    // The connection reference is dropped unless the new type takes the same
    // credential shape, for the same reason.
    const keepConn = connection && connTypeForDest(next) === wantConnType;
    onChange({
      type: next,
      ...(keepConn ? { connection } : {}),
      [nextBlock]: carried,
    });
  };

  const changeConnection = (next: string) => {
    if (next === INLINE) {
      const { connection: _dropped, ...rest } = entry;
      onChange(rest);
      return;
    }
    // Clear any connection-owned values already typed into the block, so
    // choosing a connection cannot leave behind a field that now conflicts
    // with it.
    const owned = new Set(connOwnedPaths(wantConnType ?? ""));
    const kept = Object.fromEntries(
      Object.entries(blockValue).filter(([k]) => !owned.has(k)),
    );
    onChange({ type, connection: next, [block]: kept });
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
      {wantConnType && (
        <Select
          label="Connection"
          description={
            usable.length > 0
              ? `Reuse a ${wantConnType} connection's credentials, or configure this destination inline.`
              : `No ${wantConnType} connection is defined yet. Add one in the Connections panel to share credentials.`
          }
          data={[
            { value: INLINE, label: "Configure inline" },
            ...usable.map((n) => ({ value: n, label: n })),
          ]}
          value={connection || INLINE}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => changeConnection(v ?? INLINE)}
        />
      )}
      {type === "webhook" && (
        <WebhookDeliveryNotice deliveryMode={blockValue.delivery_mode} />
      )}
      <SchemaForm
        scope={`destination/${name}`}
        schema={spec.schema}
        value={blockValue}
        onChange={(blockVal) =>
          onChange({
            type,
            ...(connection ? { connection } : {}),
            [block]: blockVal as Record<string, unknown>,
          })
        }
        fieldHelp={destFieldHelp}
        enums={
          type === "webhook" ? { delivery_mode: ["async", "sync"] } : undefined
        }
        groupObjects
        hideFields={ownedByConnection}
      />
    </Stack>
  );
}

// Renaming commits on blur rather than per keystroke. The store holds
// destinations in a plain object keyed by name, so an eager rename deletes and
// re-adds the key on every character -- which walks the row to the bottom of
// the rail while you are still typing in it.
function NameField({
  name,
  taken,
  onRename,
}: {
  name: string;
  taken: string[];
  onRename: (next: string) => void;
}) {
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
        setError(
          collides(next.trim()) ? "That destination already exists." : "",
        );
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
