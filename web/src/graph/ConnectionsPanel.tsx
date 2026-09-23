import { useState } from "react";
import { Box, Button, Modal, ScrollArea, SimpleGrid, Stack, Text, UnstyledButton } from "@mantine/core";
import { ConnectionEditor } from "./ConnectionEditor";
import { CONN_TYPES } from "./nodeSchema";
import { destinationPresentation } from "../marketing/destinationIdentity";
import { DEST_ROW_CLASS } from "./builderStyles";
import { useGraphStore } from "./store";
import { DARK } from "../tokens";

const RAIL_WIDTH = 210;

/**
 * Connections hold the credentials and connection settings a destination needs
 * to reach a backend, so one AWS account or one Postgres cluster is configured
 * in a single place and referenced by name.
 *
 * Laid out master-detail for the same reason DestinationsPanel is: a redis or
 * kafka connection has a nested TLS block, and a single scrolling column gives
 * you no landmark to navigate by.
 */
export function ConnectionsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const connections = useGraphStore((s) => s.connections);
  const setConnections = useGraphStore((s) => s.setConnections);
  const names = Object.keys(connections);
  const [picked, setPicked] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  // Derived rather than stored, so a rename or removal cannot strand the
  // selection on something that no longer exists.
  const selected = picked && names.includes(picked) ? picked : (names[0] ?? null);
  const showPicker = adding || selected === null;

  const select = (name: string) => {
    setPicked(name);
    setAdding(false);
  };

  const add = (type: string) => {
    const name = uniqueName(type, names);
    // A connection is authored flat: type sits alongside its own fields
    // rather than above a per-type block.
    setConnections({ ...connections, [name]: { type } });
    select(name);
  };

  const rename = (from: string, to: string) => {
    setConnections(Object.fromEntries(Object.entries(connections).map(([k, v]) => [k === from ? to : k, v])));
    select(to);
  };

  const remove = (name: string) => {
    const next = { ...connections };
    delete next[name];
    setConnections(next);
    const remaining = names.filter((n) => n !== name);
    setPicked(remaining[0] ?? null);
    setAdding(false);
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Connections"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Connections" }}
      styles={{ body: { padding: 0 } }}
    >
      <Text size="sm" c="dimmed" px="md" pb="sm">
        Credentials and connection settings, referenced by name from a destination&apos;s{" "}
        <Text span ff="monospace">
          connection
        </Text>{" "}
        field. Keep secrets as{" "}
        <Text span ff="monospace">
          ${"{ENV_VAR}"}
        </Text>{" "}
        references rather than literals. This panel generates config; it does not talk to a running Hookway process.
      </Text>
      <Box style={{ display: "flex", height: "70vh", minHeight: 0, borderTop: `1px solid ${DARK.border}` }}>
        <Box
          component="nav"
          aria-label="Connections"
          style={{
            width: RAIL_WIDTH,
            flexShrink: 0,
            display: "flex",
            flexDirection: "column",
            minHeight: 0,
            borderRight: `1px solid ${DARK.border}`,
            background: DARK.bg,
          }}
        >
          <ScrollArea type="auto" style={{ flex: 1, minHeight: 0 }}>
            {names.map((name) => (
              <RailRow
                key={name}
                name={name}
                type={String((connections[name] as Record<string, unknown>)?.type ?? "aws")}
                selected={!showPicker && name === selected}
                onSelect={() => select(name)}
              />
            ))}
          </ScrollArea>
          <Box p="xs" style={{ borderTop: `1px solid ${DARK.border}` }}>
            <Button size="xs" variant="light" fullWidth onClick={() => setAdding(true)}>
              Add connection
            </Button>
          </Box>
        </Box>
        <ScrollArea key={showPicker ? "__picker" : selected} type="auto" style={{ flex: 1, minWidth: 0 }} px="md" py="md">
          {showPicker ? (
            <ConnectionTypePicker onPick={add} />
          ) : (
            <ConnectionEditor
              key={selected}
              name={selected as string}
              entry={(connections[selected as string] ?? {}) as Record<string, unknown>}
              taken={names}
              onRename={(to) => rename(selected as string, to)}
              onChange={(entry) => setConnections({ ...connections, [selected as string]: entry })}
              onRemove={() => remove(selected as string)}
            />
          )}
        </ScrollArea>
      </Box>
    </Modal>
  );
}

/**
 * Driven by CONN_TYPES, so a connection type added to connectionSchema.json
 * shows up here without touching this file. Icons are reused from the
 * destination presentation table, which already falls back to a generic mark.
 */
function ConnectionTypePicker({ onPick }: { onPick: (type: string) => void }) {
  return (
    <Stack gap="sm">
      <Text size="sm" fw={600} c={DARK.text}>
        Add a connection
      </Text>
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="sm">
        {CONN_TYPES.map((type) => {
          const { label, icon: Icon } = destinationPresentation(type);
          return (
            <UnstyledButton
              key={type}
              className={DEST_ROW_CLASS}
              aria-label={`Add ${type} connection`}
              onClick={() => onPick(type)}
            >
              <Box style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                <Box component="span" c="dimmed" style={{ lineHeight: 0, flexShrink: 0 }}>
                  <Icon aria-hidden="true" focusable="false" size={16} />
                </Box>
                <Text size="xs" truncate>
                  {label}
                </Text>
              </Box>
            </UnstyledButton>
          );
        })}
      </SimpleGrid>
    </Stack>
  );
}

function RailRow({
  name,
  type,
  selected,
  onSelect,
}: {
  name: string;
  type: string;
  selected: boolean;
  onSelect: () => void;
}) {
  const { label, icon: Icon } = destinationPresentation(type);
  return (
    <UnstyledButton
      className={DEST_ROW_CLASS}
      data-selected={selected || undefined}
      aria-label={`Select ${name}`}
      aria-current={selected || undefined}
      onClick={onSelect}
    >
      <Box style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
        <Box component="span" c="dimmed" style={{ lineHeight: 0, flexShrink: 0 }}>
          <Icon aria-hidden="true" focusable="false" size={16} />
        </Box>
        <Stack gap={0} style={{ minWidth: 0 }}>
          <Text size="xs" ff="monospace" truncate>
            {name}
          </Text>
          <Text size="10px" c="dimmed" truncate>
            {label}
          </Text>
        </Stack>
      </Box>
    </UnstyledButton>
  );
}

/** Names a new connection after its type: aws, then aws_2. */
function uniqueName(type: string, taken: string[]): string {
  if (!taken.includes(type)) return type;
  let i = 2;
  while (taken.includes(`${type}_${i}`)) i += 1;
  return `${type}_${i}`;
}
