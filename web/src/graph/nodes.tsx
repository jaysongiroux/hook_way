import { nodeSummary } from "./nodeDescriptors";
import { Handle, Position, type NodeProps, type NodeTypes } from "@xyflow/react";
import { Group, Paper, Stack, Text } from "@mantine/core";
import { destinationTypeOf, NodeGlyph } from "./nodeIcon";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import type { CSSProperties } from "react";
import type { HookwayNode } from "./model";
import { labelledPorts, nodeMeta, nodeHasOnError, nodeLabel } from "./nodeSchema";
import { HANDLE_IN, HANDLE_ON_ERROR, HANDLE_OUT } from "./connection";
import { useGraphPresentation } from "./GraphPresentation";
import { DARK, NODE, NODE_SIZE, PORT } from "../tokens";

function useSelectionRing(id: string, color: string): CSSProperties {
  const selected = useGraphPresentation().selectedNodeId === id;
  return selected ? { outline: `2px solid ${color}`, outlineOffset: 2 } : {};
}

// A fixed width, not a minimum: layout.ts reserves NODE_SIZE.width per node, so
// a card that grew to fit its text would overlap the neighbour the layout had
// made room for.
const CARD: CSSProperties = {
  width: NODE_SIZE.width,
  padding: NODE_SIZE.padding,
  background: DARK.surface,
  borderColor: DARK.border,
  overflow: "hidden",
};

// The type and summary lines are single-line facts (a node type, a path, a URL),
// so they ellipsise rather than wrap. The description keeps its 2-line clamp.
const ONE_LINE: CSSProperties = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

function handleStyle(port: string): CSSProperties {
  return { background: PORT[port] ?? NODE.border.endpoint, width: NODE_SIZE.handle, height: NODE_SIZE.handle };
}

// A labelled port row is laid out in the card rather than floated over it, so
// a node with many branches grows taller instead of writing its port names on
// top of its own description. layout.ts measures the rendered card, so the
// extra height is reserved on the next Arrange.
const PORT_ROW: CSSProperties = { position: "relative", height: NODE_SIZE.portRow };

// React Flow's .react-flow__handle-right already centres the dot on its
// offset parent's right border with translate(50%). The offset parent here is
// the row, which sits NODE_SIZE.padding inside the card, so pushing it back
// out by exactly that much lands the dot on the card border where every other
// handle sits.
const PORT_ROW_HANDLE: CSSProperties = { right: -NODE_SIZE.padding };

// Port names are free text from the config, so they are clipped to the card
// rather than allowed to widen it. The full name stays on the title and in the
// Inspector.
const PORT_LABEL: CSSProperties = {
  ...ONE_LINE,
  maxWidth: NODE_SIZE.width - 2 * NODE_SIZE.padding - NODE_SIZE.handle,
  fontSize: 10,
  lineHeight: 1.2,
};

// Two lines keeps cards a predictable height on a dense canvas; the full text
// stays reachable through the title attribute and the Inspector.
const DESCRIPTION: CSSProperties = {
  display: "-webkit-box",
  WebkitBoxOrient: "vertical",
  WebkitLineClamp: 2,
  overflow: "hidden",
};

export function FlowNode({ id, data, isConnectable }: NodeProps<HookwayNode>) {
  // Handles have separate start/end switches, even when the node is not connectable.
  const connectivity = { isConnectable, isConnectableStart: isConnectable, isConnectableEnd: isConnectable };
  const meta = nodeMeta(data.nodeType, data.options);
  const input = meta?.input ?? "Envelope";
  const output = meta?.output ?? "Envelope";
  const color = PORT[output] ?? NODE.border.endpoint;
  const summary = nodeSummary(data.nodeType, data.options);
  const description = data.description?.trim() ?? "";
  const labelled = labelledPorts(data.nodeType, data.options);
  const showDefaultOut = output !== "Terminal" && labelled.length === 0 && data.nodeType !== "mcp_trigger";
  const showError = nodeHasOnError(data.nodeType);
  const destinations = useGraphPresentation().destinations;
  const destinationType =
    data.nodeType === "publish" ? destinationTypeOf(destinations, String(data.options.destination ?? "")) : "";
  const cardLabel =
    data.nodeType === "publish" && destinationType
      ? `Send to ${destinationPresentation(destinationType).label}`
      : nodeLabel(data.nodeType);
  return (
    <Paper withBorder radius="sm" style={{ ...CARD, ...useSelectionRing(id, color) }}>
      {input !== "None" && (
        <Handle {...connectivity}
          type="target"
          position={Position.Left}
          id={HANDLE_IN}
          title={input}
          style={handleStyle(input)}
        />
      )}
      <Stack gap={2}>
        <Group gap={8} wrap="nowrap" align="center" style={{ minWidth: 0 }}>
          <NodeGlyph nodeType={data.nodeType} destinationType={destinationType} />
          <Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
            <Text fw={600} size="sm" c={DARK.text} style={ONE_LINE}>
              {data.yamlId}
            </Text>
            <Text size="xs" c={DARK.muted} title={cardLabel} style={ONE_LINE}>
              {cardLabel}
            </Text>
          </Stack>
        </Group>
        {description && (
          <Text size="xs" c={DARK.text} title={description} style={DESCRIPTION}>
            {description}
          </Text>
        )}
        {summary && (
          <Text size="xs" c={DARK.muted} title={summary} style={ONE_LINE}>
            {summary}
          </Text>
        )}
        {labelled.map((port) => (
          <Group key={port} justify="flex-end" gap={6} wrap="nowrap" style={PORT_ROW}>
            <Text size="xs" c={DARK.muted} title={port} style={PORT_LABEL}>
              {port}
            </Text>
            <Handle {...connectivity}
              type="source"
              position={Position.Right}
              id={port}
              title={`${output} (${port})`}
              aria-label={port}
              style={{ ...handleStyle(output), ...PORT_ROW_HANDLE }}
            />
          </Group>
        ))}
        {showError && (
          <Text
            className="nodrag nopan"
            size="xs"
            fw={600}
            c={PORT.on_error}
            mt={6}
            pb={2}
            style={{ fontSize: 10, letterSpacing: "0.03em", textAlign: "center", lineHeight: 1.2 }}
          >
            on error
          </Text>
        )}
      </Stack>
      {showDefaultOut && (
        <Handle {...connectivity} type="source" position={Position.Right} id={HANDLE_OUT} title={output} style={handleStyle(output)} />
      )}
      {showError && (
        <Handle {...connectivity}
          type="source"
          position={Position.Bottom}
          id={HANDLE_ON_ERROR}
          title="on error — if this node fails, the same input is sent along this red handle"
          aria-label="on error"
          style={{ ...handleStyle("on_error"), bottom: -5 }}
        />
      )}
    </Paper>
  );
}

export const nodeTypes: NodeTypes = { flow: FlowNode };
