import { Box, Group, Text } from "@mantine/core";
import { LogoMark } from "../brand/Logo";
import { FlowGraphSvg } from "./FlowDiagram";
import { LAYOUT, flowDescription } from "./flowGraph";
import { DARK } from "../tokens";

// BuilderFrame shows the config builder inside browser chrome. It is rendered
// live from the same flowGraph data the diagram section uses, rather than a
// screenshot, so it stays crisp at any size and cannot go stale against the
// real UI.
export function BuilderFrame() {
  return (
    <Box
      style={{
        border: `1px solid ${DARK.border}`,
        borderRadius: 4,
        overflow: "hidden",
        background: DARK.surface,
        boxShadow: "0 8px 24px rgba(20, 24, 31, 0.08)",
      }}
    >
      <Group
        gap={6}
        px="sm"
        py={8}
        style={{ borderBottom: `1px solid ${DARK.border}`, background: DARK.recessed }}
      >
        {[0, 1, 2].map((i) => (
          <Box key={i} style={{ width: 8, height: 8, borderRadius: 1, background: DARK.border }} />
        ))}
        <LogoMark size={14} />
        <Text
          size="xs"
          c={DARK.muted}
          ml={4}
          style={{ fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace" }}
        >
          Hookway Builder
        </Text>
      </Group>
      <CanvasMock />
    </Box>
  );
}

// The canvas plus a hint of the inspector panel on the right, so the frame
// reads as an application rather than a diagram.
function CanvasMock() {
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
          <circle cx="1" cy="1" r="1" fill={DARK.border} />
        </pattern>
      </defs>
      <rect x={0} y={0} width={inspectorX - 20} height={LAYOUT.height} fill="url(#hookway-dots)" />
      <FlowGraphSvg selectedId="ep" markerId="hookway-hero-edge" />
      <Inspector x={inspectorX} />
    </svg>
  );
}

// A suggestion of the inspector: the panel the builder shows for the selected
// node. Labels only, so it reads as chrome rather than competing for attention.
function Inspector({ x }: { x: number }) {
  const rows = [
    { label: "Id", value: "in" },
    { label: "Path", value: "/webhooks/github" },
    { label: "Methods", value: "POST" },
  ];
  return (
    <g>
      <line x1={x - 20} y1={0} x2={x - 20} y2={LAYOUT.height} stroke={DARK.border} strokeWidth={1} />
      <text x={x} y={22} fontSize={10} fill={DARK.muted} letterSpacing={1}>
        HTTP endpoint
      </text>
      {rows.map((r, i) => {
        const top = 46 + i * 52;
        return (
          <g key={r.label}>
            <text x={x} y={top} fontSize={11} fill={DARK.muted}>
              {r.label}
            </text>
            <rect x={x} y={top + 8} width={210} height={26} rx={4} fill={DARK.recessed} stroke={DARK.border} />
            <text x={x + 8} y={top + 25} fontSize={11} fill={DARK.text}>
              {r.value}
            </text>
          </g>
        );
      })}
    </g>
  );
}
