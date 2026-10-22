import {
  Box,
  Button,
  Group,
  Stack,
  Text,
  TextInput,
  UnstyledButton,
} from "@mantine/core";
import { useState } from "react";
import { NODE_SCHEMAS, NODE_TYPES, nodeLabel } from "./nodeSchema";
import { nodeHelp, PALETTE_GROUP_HELP } from "./help";
import { PALETTE_ITEM_CLASS } from "./builderStyles";
import { DARK } from "../tokens";
import { TEMPLATES } from "./templates";
import { ExamplesPanel } from "./ExamplesPanel";

export const DND_MIME = "application/hookway-node";
export const DND_TEMPLATE_MIME = "application/hookway-template";

/** The panel owns the active palette section. */
export type PaletteTab = "nodes" | "templates" | "examples";

const TABS: { value: PaletteTab; label: string }[] = [
  { value: "nodes", label: "Nodes" },
  { value: "templates", label: "Templates" },
  { value: "examples", label: "Examples" },
];

const GROUPS: { title: string; types: string[] }[] = [
  {
    title: "Receive requests",
    types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "None"),
  },
  {
    title: "Check requests",
    types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RequestHead"),
  },
  {
    title: "Verify the sender",
    types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RawRequest"),
  },
  {
    title: "Prepare the event",
    types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "Verified"),
  },
  {
    title: "Process the event",
    types: NODE_TYPES.filter(
      (t) =>
        NODE_SCHEMAS[t].input === "Envelope" &&
        !NODE_SCHEMAS[t].detached &&
        NODE_SCHEMAS[t].output !== "Terminal",
    ),
  },
  {
    title: "Deliver and respond",
    types: NODE_TYPES.filter(
      (t) => NODE_SCHEMAS[t].output === "Terminal" || NODE_SCHEMAS[t].detached,
    ),
  },
];

/**
 * The palette's tab strip. It renders into the side panel's header row rather
 * than above the scrolling content, so it stays put while the list scrolls —
 * which is why it is a separate export from Palette instead of part of it, and
 * why the active tab is controlled by the panel rather than held here.
 */
export function PaletteTabs({
  value,
  onChange,
}: {
  value: PaletteTab;
  onChange: (tab: PaletteTab) => void;
}) {
  return (
    <Group role="tablist" aria-label="Palette sections" gap={2} wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
      {TABS.map((tab) => {
        const active = tab.value === value;
        return (
          <UnstyledButton
            key={tab.value}
            role="tab"
            aria-selected={active}
            onClick={() => {
              // Re-reporting the current tab would make the panel set state on
              // every click of the tab already showing.
              if (!active) onChange(tab.value);
            }}
            px={2}
            py={4}
            style={{
              borderRadius: 4,
              background: active ? DARK.surface : "transparent",
              border: `1px solid ${active ? DARK.border : "transparent"}`,
            }}
          >
            <Text
              fw={600}
              size="xs"
              c={active ? DARK.text : DARK.muted}
            >
              {tab.label}
            </Text>
          </UnstyledButton>
        );
      })}
    </Group>
  );
}

function PaletteItem({
  label,
  help,
  onDragStart,
  onAdd,
  actionLabel = "Add",
  actionName,
}: {
  label: string;
  help: string;
  onDragStart?: (e: React.DragEvent) => void;
  actionLabel?: string;
  actionName?: string;
  onAdd?: () => void;
}) {
  return (
    <Box
      className={PALETTE_ITEM_CLASS}
      p={10}
      draggable={!!onDragStart}
      onDragStart={onDragStart}
      style={{ cursor: onDragStart ? "grab" : "default" }}
    >
      <Group align="flex-start" justify="space-between" wrap="nowrap" gap="sm">
        <Text size="sm" fw={600} c={DARK.text} style={{ minWidth: 0, flex: 1 }}>
          {label}
        </Text>
        {onAdd && (
          <Button
            size="compact-xs"
            variant="subtle"
            color="brand"
            aria-label={actionName ?? `Add ${label}`}
            style={{ flexShrink: 0, minWidth: 40 }}
            onClick={onAdd}
          >
            {actionLabel}
          </Button>
        )}
      </Group>
      <Text size="xs" c={DARK.muted} lh={1.4} mt={4}>
        {help}
      </Text>
    </Box>
  );
}

/**
 * The palette body. Only the active tab's entries are rendered: a hidden tab
 * must not leave draggable elements in the DOM for a stray drag to pick up.
 */
export function Palette({
  tab,
  onAddNode,
  onAddTemplate,
  onLoadProductionTemplate,
  onExampleImported,
}: {
  tab: PaletteTab;
  onAddNode?: (type: string) => void;
  onAddTemplate?: (id: string) => void;
  onLoadProductionTemplate?: () => void;
  onExampleImported?: () => void;
}) {
  const [search, setSearch] = useState("");

  if (tab === "examples") return <ExamplesPanel onImported={onExampleImported} />;

  if (tab === "templates") {
    return (
      <Stack gap={8}>
        <Text size="xs" c={DARK.muted} lh={1.4}>
          {PALETTE_GROUP_HELP.Templates}
        </Text>
        <PaletteItem
          label="Production example"
          help="A complete GitHub webhook workspace with delivery branches and example destinations. Replaces the current workspace."
          onAdd={onLoadProductionTemplate}
          actionLabel="Load"
          actionName="Load production example"
        />
        {TEMPLATES.map((def) => (
          <PaletteItem
            key={def.id}
            label={def.label}
            help={def.help}
            onAdd={onAddTemplate ? () => onAddTemplate(def.id) : undefined}
            onDragStart={(e) => {
              e.dataTransfer.setData(DND_TEMPLATE_MIME, def.id);
              e.dataTransfer.effectAllowed = "move";
            }}
          />
        ))}
      </Stack>
    );
  }
  const query = search.trim().toLowerCase();
  const groups = GROUPS.map((group) => ({
    ...group,
    types: group.types.filter((type) => {
      if (!query) return true;
      return [type, nodeLabel(type), nodeHelp(type)].some((value) =>
        value.toLowerCase().includes(query),
      );
    }),
  })).filter((group) => group.types.length > 0);

  return (
    <Stack gap="md">
      <TextInput
        size="xs"
        aria-label="Search nodes"
        placeholder="Search nodes..."
        value={search}
        onChange={(event) => setSearch(event.currentTarget.value)}
      />
      {groups.length === 0 ? (
        <Text size="sm" c={DARK.muted} role="status">
          No nodes match “{search.trim()}”.
        </Text>
      ) : (
        <Stack gap="lg">
          {groups.map((group) => (
            <Stack key={group.title} gap={8}>
              <div>
                <Text
                  fw={600}
                  size="xs"
                  tt="uppercase"
                  c={DARK.muted}
                  style={{ letterSpacing: "0.08em" }}
                >
                  {group.title}
                </Text>
                {PALETTE_GROUP_HELP[group.title] && (
                  <Text size="xs" c={DARK.muted} lh={1.4} mt={4}>
                    {PALETTE_GROUP_HELP[group.title]}
                  </Text>
                )}
              </div>
              {group.types.map((type) => (
                <PaletteItem
                  key={type}
                  label={nodeLabel(type)}
                  help={nodeHelp(type)}
                  onAdd={onAddNode ? () => onAddNode(type) : undefined}
                  onDragStart={(e) => {
                    e.dataTransfer.setData(DND_MIME, type);
                    e.dataTransfer.effectAllowed = "move";
                  }}
                />
              ))}
            </Stack>
          ))}
        </Stack>
      )}
    </Stack>
  );
}
