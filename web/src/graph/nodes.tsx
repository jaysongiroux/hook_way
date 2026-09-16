import { Handle, Position, type NodeProps, type NodeTypes } from "@xyflow/react";
import { Paper, Stack, Text } from "@mantine/core";
import type { CSSProperties } from "react";
import type { HookwayNode } from "./model";
import { NODE_SCHEMAS, nodeLabel, switchPorts } from "./nodeSchema";
import { HANDLE_IN, HANDLE_ON_ERROR, HANDLE_OUT } from "./connection";
import { useGraphStore } from "./store";
import { DARK, NODE, PORT } from "../tokens";

function useSelectionRing(id: string, color: string): CSSProperties {
  const selected = useGraphStore((s) => s.selectedId === id);
  return selected ? { outline: `2px solid ${color}`, outlineOffset: 2 } : {};
}

const CARD: CSSProperties = { minWidth: 168, background: DARK.surface };

function handleStyle(port: string): CSSProperties {
  return { background: PORT[port] ?? NODE.border.endpoint, width: 10, height: 10 };
}

export function FlowNode({ id, data }: NodeProps<HookwayNode>) {
  const meta = NODE_SCHEMAS[data.nodeType];
  const input = meta?.input ?? "Envelope";
  const output = meta?.output ?? "Envelope";
  const color = PORT[output] ?? NODE.border.endpoint;
  const summary = nodeSummary(data.nodeType, data.options);
  const labelled = data.nodeType === "switch" ? switchPorts(data.options) : [];
  const showDefaultOut = output !== "Terminal" && labelled.length === 0;
  const showError = input !== "None";
  return (
    <Paper withBorder radius="md" p="xs" style={{ ...CARD, borderColor: color, ...useSelectionRing(id, color) }}>
      {input !== "None" && (
        <Handle
          type="target"
          position={Position.Left}
          id={HANDLE_IN}
          title={input}
          style={handleStyle(input)}
        />
      )}
      <Stack gap={2}>
        <Text fw={600} size="sm" c={DARK.text}>
          {data.yamlId}
        </Text>
        <Text size="xs" c={PORT[output] ?? DARK.accentText} style={{ fontFamily: "ui-monospace, Menlo, monospace" }}>
          {nodeLabel(data.nodeType)}
        </Text>
        {summary && (
          <Text size="xs" c={DARK.muted}>
            {summary}
          </Text>
        )}
      </Stack>
      {showDefaultOut && (
        <Handle type="source" position={Position.Right} id={HANDLE_OUT} title={output} style={handleStyle(output)} />
      )}
      {labelled.map((port, i) => (
        <Handle
          key={port}
          type="source"
          position={Position.Right}
          id={port}
          title={`${output} (${port})`}
          style={{
            ...handleStyle(output),
            top: `${((i + 1) / (labelled.length + 1)) * 100}%`,
          }}
        />
      ))}
      {showError && (
        <>
          <Text
            className="nodrag nopan"
            size="xs"
            fw={600}
            c={PORT.on_error}
            mt={8}
            style={{ fontSize: 10, letterSpacing: "0.03em", textAlign: "center", lineHeight: 1.2 }}
          >
            on error
          </Text>
          <Handle
            type="source"
            position={Position.Bottom}
            id={HANDLE_ON_ERROR}
            title="on error — if this node fails, the same input is sent along this red handle"
            aria-label="on error"
            style={{ ...handleStyle("on_error"), bottom: -5 }}
          />
        </>
      )}
    </Paper>
  );
}

function nodeSummary(type: string, options: Record<string, unknown>): string {
  if (type === "http_trigger") return String(options.path ?? "");
  if (type === "publish") return [options.destination, options.target].filter(Boolean).map(String).join(" → ");
  if (type === "auth") return String(options.scheme ?? "");
  if (type === "filter") {
    const conditions = Array.isArray(options.conditions) ? options.conditions : [];
    const first = conditions[0] as { path?: string; operator?: string } | undefined;
    return first?.path ? `${first.path} ${first.operator ?? ""}`.trim() : "";
  }
  if (type === "http_request") return String(options.url ?? "");
  if (type === "respond") return options.passthrough ? "passthrough" : String(options.status ?? "");
  return "";
}

export const nodeTypes: NodeTypes = { flow: FlowNode };
