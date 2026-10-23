import { Box, Button, Group, Text } from "@mantine/core";
import { useState } from "react";
import { LogoMark } from "../brand/Logo";
import { FlowGraphSvg } from "./FlowDiagram";
import { LAYOUT, flowDescription } from "./flowGraph";
import { DARK } from "../tokens";
import { useVisibleMotion } from "./useVisibleMotion";

// BuilderFrame shows the config builder inside browser chrome. It is rendered
// from the same flowGraph data the diagram section uses, so the illustrative
// example stays consistent across the page.
export function BuilderFrame() {
  const { viewportRef, running } = useVisibleMotion();
  const [paused, setPaused] = useState(false);
  return (
    <Box
      ref={viewportRef}
      className="hookway-hero-diagram"
      data-running={running && !paused ? "true" : "false"}
      style={{
        border: `1px solid ${DARK.border}`,
        borderRadius: 12,
        boxShadow: "0 8px 32px rgba(30, 50, 75, 0.06), 0 1px 3px rgba(30, 50, 75, 0.04)",
        overflow: "hidden",
        background: DARK.surface,
      }}
    >
      <Group
        gap={6}
        px="md"
        py={10}
        style={{ borderBottom: `1px solid ${DARK.border}`, background: DARK.surface }}
      >
        <LogoMark size={17} />
        <Text
          size="xs"
          c={DARK.muted}
          ml={4}
          style={{ fontFamily: '"Source Code Pro", ui-monospace, Menlo, monospace' }}
        >
          Hookway Builder
        </Text>
        <Button
          className="hookway-flow-motion-control"
          variant="subtle"
          size="compact-xs"
          ml="auto"
          aria-pressed={paused}
          onClick={() => setPaused((value) => !value)}
        >
          {paused ? "Resume diagram motion" : "Pause diagram motion"}
        </Button>
      </Group>
      <Box className="hookway-hero-canvas" tabIndex={0} role="region" aria-label="Example flow diagram">
        <CanvasMock />
      </Box>
      <Text className="hookway-flow-scroll-hint" size="xs" c={DARK.muted} px="sm" py={8}>
        Scroll to see the destinations.
      </Text>
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
        <pattern id="hookway-dots" width="16" height="16" patternUnits="userSpaceOnUse">
          <circle cx="0.6" cy="0.6" r="0.65" fill="#DEE4EC" />
        </pattern>
      </defs>
      <rect x={0} y={0} width={inspectorX - 20} height={LAYOUT.height} fill="#F8FAFD" />
      <rect x={0} y={0} width={inspectorX - 20} height={LAYOUT.height} fill="url(#hookway-dots)" />
      <FlowGraphSvg selectedId="ep" markerId="hookway-hero-edge" animate />
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
      <rect x={x - 20} y={0} width={270} height={LAYOUT.height} fill="#FFFFFF" />
      <line x1={x - 20} y1={0} x2={x - 20} y2={LAYOUT.height} stroke={DARK.border} strokeWidth={1} />
      <text x={x} y={36} fontSize={15} fontWeight={650} fill={DARK.text}>
        in
      </text>
      <text x={x} y={54} fontSize={11} fill={DARK.accent}>
        HTTP endpoint
      </text>
      <line x1={x} y1={70} x2={x + 210} y2={70} stroke="#E6EAF0" />
      {rows.map((r, i) => {
        const top = 91 + i * 55;
        return (
          <g key={r.label}>
            <text x={x} y={top} fontSize={11} fill={DARK.muted}>
              {r.label}
            </text>
            <rect x={x} y={top + 6} width={210} height={29} rx={5} fill="#F8FAFD" stroke="#DDE3EB" />
            <text x={x + 8} y={top + 25} fontSize={11} fill={DARK.text}>
              {r.value}
            </text>
          </g>
        );
      })}
    </g>
  );
}
