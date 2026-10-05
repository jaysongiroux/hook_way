import { useState, type ComponentType, type ReactNode } from "react";
import { Box, Button, Modal, ScrollArea, Stack, Text, UnstyledButton } from "@mantine/core";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { DEST_ROW_CLASS } from "./builderStyles";
import { DARK } from "../tokens";

const RAIL_WIDTH = 210;

type NamedConfigPanelProps = {
  opened: boolean;
  onClose: () => void;
  title: string;
  addLabel: string;
  description: ReactNode;
  entries: Record<string, unknown>;
  onChange: (entries: Record<string, unknown>) => void;
  defaultType: string;
  createEntry: (type: string) => Record<string, unknown>;
  Picker: ComponentType<{ onPick: (type: string) => void }>;
  Editor: ComponentType<{
    name: string;
    entry: Record<string, unknown>;
    taken: string[];
    onRename: (name: string) => void;
    onChange: (entry: Record<string, unknown>) => void;
    onRemove: () => void;
  }>;
};

/**
 * Shared named-entry controls with independently scrolling rail and detail.
 * Keying the detail by selection resets its form and scroll position on switch.
 * Editors and entry factories keep their backend-specific configuration shapes.
 */
export function NamedConfigPanel({
  opened,
  onClose,
  title,
  addLabel,
  description,
  entries,
  onChange,
  defaultType,
  createEntry,
  Picker,
  Editor,
}: NamedConfigPanelProps) {
  const names = Object.keys(entries);
  const [picked, setPicked] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  // A rename, removal or external import cannot leave the selection stranded.
  const selected = picked && names.includes(picked) ? picked : (names[0] ?? null);
  const showPicker = adding || selected === null;

  const select = (name: string) => {
    setPicked(name);
    setAdding(false);
  };

  const add = (type: string) => {
    const name = uniqueName(type, names);
    onChange({ ...entries, [name]: createEntry(type) });
    select(name);
  };

  const rename = (from: string, to: string) => {
    onChange(Object.fromEntries(Object.entries(entries).map(([k, v]) => [k === from ? to : k, v])));
    select(to);
  };

  const remove = (name: string) => {
    const next = { ...entries };
    delete next[name];
    onChange(next);
    const remaining = names.filter((n) => n !== name);
    setPicked(remaining[0] ?? null);
    setAdding(false);
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={title}
      size="xl"
      closeButtonProps={{ "aria-label": `Close ${title}` }}
      styles={{ body: { padding: 0 } }}
    >
      <Text size="sm" c="dimmed" px="md" pb="sm">
        {description}
      </Text>
      <Box style={{ display: "flex", height: "70vh", minHeight: 0, borderTop: `1px solid ${DARK.border}` }}>
        <Box
          component="nav"
          aria-label={title}
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
                type={String((entries[name] as Record<string, unknown>)?.type ?? defaultType)}
                selected={!showPicker && name === selected}
                onSelect={() => select(name)}
              />
            ))}
          </ScrollArea>
          <Box p="xs" style={{ borderTop: `1px solid ${DARK.border}` }}>
            <Button size="xs" variant="light" fullWidth onClick={() => setAdding(true)}>
              {addLabel}
            </Button>
          </Box>
        </Box>
        <ScrollArea key={showPicker ? "__picker" : selected} type="auto" style={{ flex: 1, minWidth: 0 }} px="md" py="md">
          {showPicker ? (
            <Picker onPick={add} />
          ) : (
            <Editor
              key={selected}
              name={selected as string}
              entry={(entries[selected as string] ?? {}) as Record<string, unknown>}
              taken={names}
              onRename={(to) => rename(selected as string, to)}
              onChange={(entry) => onChange({ ...entries, [selected as string]: entry })}
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

/** Names new entries after their type, adding a suffix when needed. */
function uniqueName(type: string, taken: string[]): string {
  if (!taken.includes(type)) return type;
  let i = 2;
  while (taken.includes(`${type}_${i}`)) i += 1;
  return `${type}_${i}`;
}
