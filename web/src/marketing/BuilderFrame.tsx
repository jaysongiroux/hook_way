import { Box, Group, Text } from "@mantine/core";
import { FLOW_EDGES, FLOW_NODES, LAYOUT, edgePath, flowDescription, nodeBox, type FlowNode } from "./flowGraph";
import { DARK, EDGE, NODE } from "../tokens";

// BuilderFrame shows the config builder inside browser chrome. It is rendered
// live from the same flowGraph data the diagram section uses, rather than a
// screenshot, so it stays crisp at any size and cannot go stale against the
// real UI.
export function BuilderFrame() {
  return (
    <Box
      style={{
        border: `1px solid ${DARK.border}`,
        borderRadius: 12,
        overflow: "hidden",
        background: DARK.surface,
        boxShadow: "0 24px 60px rgba(0, 0, 0, 0.45)",
      }}
    >
      <Group
        gap={6}
        px="sm"
        py={8}
        style={{ borderBottom: `1px solid ${DARK.border}`, background: DARK.recessed }}
      >
        {["#FF5F57", "#FEBC2E", "#28C840"].map((c) => (
          <Box key={c} style={{ width: 10, height: 10, borderRadius: 5, background: c }} />
        ))}
        <Text
          size="xs"
          c={DARK.muted}
          ml="sm"
          style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" }}
        >
          localhost:8090/builder
        </Text>
      </Group>
      <CanvasMock />
    </Box>
  );
}

// The canvas plus a hint of the inspector panel on the right, so the frame
// reads as an application rather than a diagram.
function CanvasMock() {
  const nodeById = (id: string) => FLOW_NODES.find((n) => n.id === id)!;
  const inspectorX = LAYOUT.width + 40;
  const totalWidth = inspectorX + 250;
  return (
    <svg
      role="img"
      aria-label={`The Hookway config builder. ${flowDescription()}`}
      viewBox={`0 0 ${totalWidth} ${LAYOUT.height}`}
      width="100%"
      style={{ display: "block", fontFamily: "inherit" }}
    >
      <defs>
        <pattern id="hookway-dots" width="18" height="18" patternUnits="userSpaceOnUse">
          <circle cx="1" cy="1" r="1" fill="#2A3A57" />
        </pattern>
      </defs>
      <rect x={0} y={0} width={inspectorX - 20} height={LAYOUT.height} fill="url(#hookway-dots)" />

      {FLOW_EDGES.map((e) => (
        <path
          key={`${e.from}-${e.to}`}
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          fill="none"
          stroke={EDGE.line}
          strokeWidth={1.5}
        />
      ))}
      {FLOW_EDGES.map((e, i) => (
        <path
          key={`pulse-${e.from}-${e.to}`}
          className="hookway-pulse"
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          fill="none"
          stroke={DARK.highlight}
          strokeWidth={2.5}
          strokeLinecap="round"
          style={{ animationDelay: `${i * 0.45}s` }}
        />
      ))}
      {FLOW_NODES.map((n) => (
        <DarkNode key={n.id} node={n} selected={n.id === "ep"} />
      ))}

      <Inspector x={inspectorX} />
    </svg>
  );
}

function DarkNode({ node, selected }: { node: FlowNode; selected: boolean }) {
  const b = nodeBox(node);
  return (
    <g>
      {/* Border by node kind, from the same palette the real builder uses, so
          the frame is a faithful preview rather than a lookalike. */}
      <rect
        x={b.x}
        y={b.y}
        width={b.w}
        height={b.h}
        rx={8}
        fill={DARK.surface}
        stroke={NODE.border[node.kind]}
        strokeWidth={selected ? 2 : 1}
      />
      <rect x={b.x + 12} y={b.y + 12} width={24} height={3} rx={1.5} fill={node.accent} />
      <text x={b.x + 12} y={b.y + 32} fontSize={13} fontWeight={600} fill={NODE.label[node.kind]}>
        {node.label}
      </text>
      <text x={b.x + 12} y={b.y + 48} fontSize={11} fill={DARK.muted}>
        {node.sub}
      </text>
    </g>
  );
}

// A suggestion of the inspector: the panel the builder shows for the selected
// node. Labels only, so it reads as chrome rather than competing for attention.
function Inspector({ x }: { x: number }) {
  const rows = [
    { label: "Path", value: "/webhooks/{envName}/digifi" },
    { label: "Auth", value: "hmac_sha256" },
    { label: "Variables", value: "2" },
    { label: "Routes", value: "3" },
  ];
  return (
    <g>
      <line x1={x - 20} y1={0} x2={x - 20} y2={LAYOUT.height} stroke={DARK.border} strokeWidth={1} />
      <text x={x} y={22} fontSize={10} fill={DARK.muted} letterSpacing={1}>
        INSPECTOR
      </text>
      {rows.map((r, i) => {
        const top = 46 + i * 52;
        return (
          <g key={r.label}>
            <text x={x} y={top} fontSize={11} fill={DARK.muted}>
              {r.label}
            </text>
            <rect x={x} y={top + 8} width={210} height={26} rx={5} fill={DARK.recessed} stroke={DARK.border} />
            <text x={x + 8} y={top + 25} fontSize={11} fill={DARK.text}>
              {r.value}
            </text>
          </g>
        );
      })}
    </g>
  );
}
