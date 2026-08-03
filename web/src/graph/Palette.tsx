import { Paper, Stack, Text } from "@mantine/core";
import type { NodeKind } from "./model";

export const DND_MIME = "application/hookway-node";

const items: { kind: NodeKind; label: string }[] = [
  { kind: "endpoint", label: "Endpoint" },
  { kind: "filter", label: "Filter" },
  { kind: "destination", label: "Destination" },
];

export function Palette() {
  return (
    <Stack gap="xs">
      {items.map((it) => (
        <Paper
          key={it.kind}
          withBorder
          p="xs"
          radius="sm"
          draggable
          onDragStart={(e) => {
            e.dataTransfer.setData(DND_MIME, it.kind);
            e.dataTransfer.effectAllowed = "move";
          }}
          style={{ cursor: "grab" }}
        >
          <Text size="sm">{it.label}</Text>
        </Paper>
      ))}
    </Stack>
  );
}
