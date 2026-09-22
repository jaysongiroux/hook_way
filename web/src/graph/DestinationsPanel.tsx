import { useState } from "react";
import { Box, Button, Modal, ScrollArea, Stack, Text, UnstyledButton } from "@mantine/core";
import { DestinationEditor } from "./DestinationEditor";
import { DestinationTypePicker } from "./DestinationTypePicker";
import { destSchema } from "./nodeSchema";
import { destinationPresentation } from "../marketing/destinationIdentity";
import { DEST_ROW_CLASS } from "./builderStyles";
import { useGraphStore } from "./store";
import { DARK } from "../tokens";

const RAIL_WIDTH = 210;

/**
 * Destinations are edited master-detail: a rail of names on the left, one
 * destination's form on the right. Every destination used to be expanded in a
 * single column, which for a backend like rabbitmq -- eleven fields and four
 * nested blocks -- meant thousands of pixels of scroll with no landmark to
 * navigate by.
 *
 * The two panes scroll independently, so the rail stays put while a long form
 * moves, and the detail pane is keyed on the selected name so switching
 * destinations lands you at the top of the new form rather than halfway down it.
 */
export function DestinationsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const destinations = useGraphStore((s) => s.destinations);
  const setDestinations = useGraphStore((s) => s.setDestinations);
  const names = Object.keys(destinations);
  const [picked, setPicked] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  // Derived rather than stored: a destination can be renamed or removed from
  // under the selection, and falling back here keeps the pane on something real
  // without an effect that fires after a frame of empty pane.
  const selected = picked && names.includes(picked) ? picked : (names[0] ?? null);
  const showPicker = adding || selected === null;

  const select = (name: string) => {
    setPicked(name);
    setAdding(false);
  };

  const add = (type: string) => {
    const name = uniqueName(type, names);
    const block = destSchema(type)?.block ?? type;
    setDestinations({ ...destinations, [name]: { type, [block]: {} } });
    select(name);
  };

  const rename = (from: string, to: string) => {
    setDestinations(Object.fromEntries(Object.entries(destinations).map(([k, v]) => [k === from ? to : k, v])));
    select(to);
  };

  const remove = (name: string) => {
    const next = { ...destinations };
    delete next[name];
    setDestinations(next);
    const remaining = names.filter((n) => n !== name);
    setPicked(remaining[0] ?? null);
    setAdding(false);
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Destinations"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Destinations" }}
      styles={{ body: { padding: 0 } }}
    >
      <Text size="sm" c="dimmed" px="md" pb="sm">
        Shared targets referenced by <Text span ff="monospace">publish</Text> nodes. This panel generates config; it does
        not talk to a running Hookway process.
      </Text>
      <Box style={{ display: "flex", height: "70vh", minHeight: 0, borderTop: `1px solid ${DARK.border}` }}>
        <Box
          component="nav"
          aria-label="Destinations"
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
                type={String((destinations[name] as Record<string, unknown>)?.type ?? "webhook")}
                selected={!showPicker && name === selected}
                onSelect={() => select(name)}
              />
            ))}
          </ScrollArea>
          <Box p="xs" style={{ borderTop: `1px solid ${DARK.border}` }}>
            <Button size="xs" variant="light" fullWidth onClick={() => setAdding(true)}>
              Add destination
            </Button>
          </Box>
        </Box>
        <ScrollArea key={showPicker ? "__picker" : selected} type="auto" style={{ flex: 1, minWidth: 0 }} px="md" py="md">
          {showPicker ? (
            <DestinationTypePicker onPick={add} />
          ) : (
            <DestinationEditor
              key={selected}
              name={selected as string}
              entry={(destinations[selected as string] ?? {}) as Record<string, unknown>}
              taken={names}
              onRename={(to) => rename(selected as string, to)}
              onChange={(entry) => setDestinations({ ...destinations, [selected as string]: entry })}
              onRemove={() => remove(selected as string)}
            />
          )}
        </ScrollArea>
      </Box>
    </Modal>
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

/** Names a new destination after its backend: webhook, then webhook_2. */
function uniqueName(type: string, taken: string[]): string {
  if (!taken.includes(type)) return type;
  let i = 2;
  while (taken.includes(`${type}_${i}`)) i += 1;
  return `${type}_${i}`;
}
