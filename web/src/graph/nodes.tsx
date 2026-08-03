import { Handle, Position, type NodeProps, type NodeTypes } from "@xyflow/react";
import { Paper, Stack, Text, Badge } from "@mantine/core";
import type { HookwayNode, EndpointData, DestinationData, FilterData } from "./model";

export function EndpointNode({ data }: NodeProps<HookwayNode>) {
  const d = data as EndpointData;
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#366CED", minWidth: 150 }}>
      <Stack gap={2}>
        <Text fw={600} size="sm" c="brand.6">{d.name}</Text>
        <Text size="xs" c="dimmed">{d.path}</Text>
      </Stack>
      <Handle type="source" position={Position.Right} />
    </Paper>
  );
}

export function DestinationNode({ data }: NodeProps<HookwayNode>) {
  const d = data as DestinationData;
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#434C5E", minWidth: 150 }}>
      <Handle type="target" position={Position.Left} />
      <Stack gap={4}>
        <Text fw={600} size="sm">{d.name}</Text>
        <Badge color="#434C5E" variant="light" size="sm">{d.destType}</Badge>
      </Stack>
    </Paper>
  );
}

export function FilterNode({ data }: NodeProps<HookwayNode>) {
  const d = data as FilterData;
  const summary = d.path ? `${d.source}.${d.path} ${d.operator} ${String(d.value)}` : "no condition";
  return (
    <Paper withBorder radius="md" p="xs" style={{ borderColor: "#25BE8A", minWidth: 150 }}>
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
