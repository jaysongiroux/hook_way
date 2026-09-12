import { Handle, Position, type NodeProps, type NodeTypes } from "@xyflow/react";
import { Paper, Stack, Text } from "@mantine/core";
import type { CSSProperties } from "react";
import type { HookwayNode, EndpointData, DestinationData, FilterData } from "./model";
import { useGraphStore } from "./store";
import { DARK, NODE } from "../tokens";

// useSelectionRing returns the style overlay that outlines the node the user
// is currently editing (store.selectedId), in the node's own accent color so
// it reads as "this one is selected" without hiding the node's identity color.
function useSelectionRing(id: string, color: string): CSSProperties {
  const selected = useGraphStore((s) => s.selectedId === id);
  return selected ? { outline: `2px solid ${color}`, outlineOffset: 2 } : {};
}

const CARD: CSSProperties = { minWidth: 150, background: DARK.surface };

export function EndpointNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as EndpointData;
  return (
    <Paper
      withBorder
      radius="md"
      p="xs"
      style={{ ...CARD, borderColor: NODE.border.endpoint, ...useSelectionRing(id, NODE.border.endpoint) }}
    >
      <Stack gap={2}>
        {/* The brand blue is a 3.2:1 fill colour, too low for 13px text, so
            labels use the lighter step of the same hue. */}
        <Text fw={600} size="sm" c={NODE.label.endpoint}>
          {d.name}
        </Text>
        <Text size="xs" c={DARK.muted}>
          {d.path}
        </Text>
      </Stack>
      <Handle type="source" position={Position.Right} />
    </Paper>
  );
}

export function DestinationNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as DestinationData;
  return (
    <Paper
      withBorder
      radius="md"
      p="xs"
      style={{ ...CARD, borderColor: NODE.border.destination, ...useSelectionRing(id, NODE.border.destination) }}
    >
      <Handle type="target" position={Position.Left} />
      <Stack gap={6}>
        <Text fw={600} size="sm" c={NODE.label.destination}>
          {d.name}
        </Text>
        {/* A Mantine "light" badge of an already-dark slate rendered dark text
            on a dark tint. The type is spelled out explicitly instead. */}
        <Text
          size="xs"
          fw={500}
          c={NODE.badgeText}
          style={{
            background: NODE.badgeBg,
            borderRadius: 4,
            padding: "2px 6px",
            alignSelf: "flex-start",
            fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
          }}
        >
          {d.destType}
        </Text>
      </Stack>
    </Paper>
  );
}

export function FilterNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as FilterData;
  const summary = d.path ? `${d.source}.${d.path} ${d.operator} ${String(d.value)}` : "no condition";
  return (
    <Paper
      withBorder
      radius="md"
      p="xs"
      style={{ ...CARD, borderColor: NODE.border.filter, ...useSelectionRing(id, NODE.border.filter) }}
    >
      <Handle type="target" position={Position.Left} />
      <Stack gap={2}>
        <Text fw={600} size="sm" c={NODE.label.filter}>
          {d.name}
        </Text>
        <Text size="xs" c={DARK.muted}>
          {summary}
        </Text>
      </Stack>
      <Handle type="source" position={Position.Right} />
    </Paper>
  );
}

export const nodeTypes: NodeTypes = {
  endpoint: EndpointNode,
  destination: DestinationNode,
  filter: FilterNode,
};
