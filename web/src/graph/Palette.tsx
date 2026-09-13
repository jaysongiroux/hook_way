import { Box, Group, Paper, Stack, Text } from "@mantine/core";
import type { CSSProperties } from "react";
import type { NodeKind } from "./model";
import { NODE } from "../tokens";
import { BUILDER_PALETTE_ITEM_CLASS } from "../builderStyles";

export const DND_MIME = "application/hookway-node";

const items: { kind: NodeKind; label: string; description: string; color: string }[] = [
  { kind: "endpoint", label: "Endpoint", description: "Receive and verify", color: NODE.border.endpoint },
  { kind: "filter", label: "Filter", description: "Match a condition", color: NODE.border.filter },
  { kind: "destination", label: "Destination", description: "Deliver the event", color: NODE.border.destination },
];

export function Palette() {
  return (
    <Stack gap="xs">
      {items.map((it) => (
        <Paper
          key={it.kind}
          withBorder
          px="sm"
          py={10}
          radius="md"
          draggable
          className={BUILDER_PALETTE_ITEM_CLASS}
          onDragStart={(e) => {
            e.dataTransfer.setData(DND_MIME, it.kind);
            e.dataTransfer.effectAllowed = "move";
          }}
          style={{ cursor: "grab", "--node-accent": it.color } as CSSProperties}
        >
          <Group gap={10} wrap="nowrap">
            <Box
              aria-hidden="true"
              style={{ width: 8, height: 8, flexShrink: 0, borderRadius: 999, background: it.color, boxShadow: `0 0 0 4px ${it.color}18` }}
            />
            <Box style={{ minWidth: 0 }}>
              <Text size="sm" fw={600} c="white">{it.label}</Text>
              <Text size="xs" c="dimmed">{it.description}</Text>
            </Box>
          </Group>
        </Paper>
      ))}
    </Stack>
  );
}
