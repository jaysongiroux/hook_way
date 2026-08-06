import { Handle, Position, type NodeProps, type NodeTypes } from "@xyflow/react";
import { Paper, Stack, Text, Badge } from "@mantine/core";
import type { CSSProperties } from "react";
import type { HookwayNode, EndpointData, DestinationData, FilterData } from "./model";
import { useGraphStore } from "./store";

// useSelectionRing returns the style overlay that outlines the node the user
// is currently editing (store.selectedId), in the node's own accent color so
// it reads as "this one is selected" without hiding the node's identity color.
function useSelectionRing(id: string, color: string): CSSProperties {
  const selected = useGraphStore((s) => s.selectedId === id);
  return selected ? { outline: `2px solid ${color}`, outlineOffset: 2 } : {};
}

export function EndpointNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as EndpointData;
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#366CED", minWidth: 150, ...useSelectionRing(id, "#366CED") }}>
      <Stack gap={2}>
        <Text fw={600} size="sm" c="brand.6">{d.name}</Text>
        <Text size="xs" c="dimmed">{d.path}</Text>
      </Stack>
      <Handle type="source" position={Position.Right} />
    </Paper>
  );
}

export function DestinationNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as DestinationData;
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#434C5E", minWidth: 150, ...useSelectionRing(id, "#434C5E") }}>
      <Handle type="target" position={Position.Left} />
      <Stack gap={4}>
        <Text fw={600} size="sm">{d.name}</Text>
        <Badge color="#434C5E" variant="light" size="sm">{d.destType}</Badge>
      </Stack>
    </Paper>
  );
}

export function FilterNode({ id, data }: NodeProps<HookwayNode>) {
  const d = data as FilterData;
  const summary = d.path ? `${d.source}.${d.path} ${d.operator} ${String(d.value)}` : "no condition";
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#25BE8A", minWidth: 150, ...useSelectionRing(id, "#25BE8A") }}>
      <Handle type="target" position={Position.Left} />
      <Stack gap={2}>
        <Text fw={600} size="sm">{d.name}</Text>
        <Text size="xs" c="dimmed">{summary}</Text>
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
