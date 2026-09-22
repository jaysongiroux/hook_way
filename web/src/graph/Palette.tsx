import { Box, Group, Stack, Text, UnstyledButton } from "@mantine/core";
import { NODE_SCHEMAS, NODE_TYPES, nodeLabel } from "./nodeSchema";
import { nodeHelp, PALETTE_GROUP_HELP } from "./help";
import { PALETTE_ITEM_CLASS } from "./builderStyles";
import { DARK } from "../tokens";
import { TEMPLATES } from "./templates";

export const DND_MIME = "application/hookway-node";
export const DND_TEMPLATE_MIME = "application/hookway-template";

/** Which half of the palette is showing. The panel owns the value. */
export type PaletteTab = "nodes" | "templates";

const TABS: { value: PaletteTab; label: string }[] = [
  { value: "nodes", label: "Nodes" },
  { value: "templates", label: "Templates" },
];

const GROUPS: { title: string; types: string[] }[] = [
  { title: "Trigger", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "None") },
  { title: "Request head", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RequestHead") },
  { title: "Raw body", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "RawRequest") },
  { title: "Verified", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "Verified") },
  { title: "Envelope", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].input === "Envelope" && !NODE_SCHEMAS[t].detached && NODE_SCHEMAS[t].output !== "Terminal") },
  { title: "Terminal", types: NODE_TYPES.filter((t) => NODE_SCHEMAS[t].output === "Terminal" || NODE_SCHEMAS[t].detached) },
];

/**
 * The palette's tab strip. It renders into the side panel's header row rather
 * than above the scrolling content, so it stays put while the list scrolls —
 * which is why it is a separate export from Palette instead of part of it, and
 * why the active tab is controlled by the panel rather than held here.
 */
export function PaletteTabs({ value, onChange }: { value: PaletteTab; onChange: (tab: PaletteTab) => void }) {
  return (
    <Group role="tablist" aria-label="Palette sections" gap={4} wrap="nowrap">
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
            px={8}
            py={4}
            style={{
              borderRadius: 4,
              background: active ? DARK.surface : "transparent",
              border: `1px solid ${active ? DARK.border : "transparent"}`,
            }}
          >
            <Text fw={600} size="xs" tt="uppercase" c={active ? DARK.text : DARK.muted} style={{ letterSpacing: "0.08em" }}>
              {tab.label}
            </Text>
          </UnstyledButton>
        );
      })}
    </Group>
  );
}

function PaletteItem({ label, help, onDragStart }: { label: string; help: string; onDragStart: (e: React.DragEvent) => void }) {
  return (
    <Box className={PALETTE_ITEM_CLASS} draggable onDragStart={onDragStart}>
      <Box px={10} py={8}>
        <Text size="sm" fw={600} c={DARK.text}>
          {label}
        </Text>
        <Text size="xs" c={DARK.muted} lh={1.4}>
          {help}
        </Text>
      </Box>
    </Box>
  );
}

/**
 * The palette body. Only the active tab's entries are rendered: a hidden tab
 * must not leave draggable elements in the DOM for a stray drag to pick up.
 */
export function Palette({ tab }: { tab: PaletteTab }) {
  if (tab === "templates") {
    return (
      <Stack gap={8}>
        <Text size="xs" c={DARK.muted} lh={1.4}>
          {PALETTE_GROUP_HELP.Templates}
        </Text>
        {TEMPLATES.map((def) => (
          <PaletteItem
            key={def.id}
            label={def.label}
            help={def.help}
            onDragStart={(e) => {
              e.dataTransfer.setData(DND_TEMPLATE_MIME, def.id);
              e.dataTransfer.effectAllowed = "move";
            }}
          />
        ))}
      </Stack>
    );
  }
  return (
    <Stack gap="lg">
      {GROUPS.map((group) =>
        group.types.length === 0 ? null : (
          <Stack key={group.title} gap={8}>
            <div>
              <Text fw={600} size="xs" tt="uppercase" c={DARK.muted} style={{ letterSpacing: "0.08em" }}>
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
                onDragStart={(e) => {
                  e.dataTransfer.setData(DND_MIME, type);
                  e.dataTransfer.effectAllowed = "move";
                }}
              />
            ))}
          </Stack>
        ),
      )}
    </Stack>
  );
}
