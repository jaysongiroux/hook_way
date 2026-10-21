import { Card, Stack, Text } from "@mantine/core";
import type { CSSProperties } from "react";
import { useMediaQuery } from "@mantine/hooks";
import {
  FLOW_EDGES,
  FLOW_NODES,
  LAYOUT,
  edgePath,
  flowDescription,
  nodeBox,
  nodeChrome,
  type FlowNode,
} from "./flowGraph";
import { Section } from "./Section";
import { nodeLabel } from "../graph/nodeSchema";
import { DARK, EDGE, NODE, PORT } from "../tokens";

const MONO = "ui-monospace, Menlo, monospace";

// FlowDiagram shows one verified flow fanning out to three different
// backends, with route-level filters gating two of the branches. Drawn as
// inline SVG rather than embedding the builder's canvas, so the overview does
// not pay for React Flow.
export function FlowDiagram() {
  // One variant renders at a time, so the labels appear once in the DOM and
  // once in the accessibility tree.
  const wide = useMediaQuery("(min-width: 48em)", true);
  return (
    <Section
      title="Fan out without duplicate handlers"
      lead="A condition can stop one branch without blocking delivery on the others."
    >
      {wide ? <DiagramSvg /> : <DiagramCards />}
    </Section>
  );
}

function DiagramSvg() {
  return (
    <svg
      role="img"
      aria-label={flowDescription()}
      viewBox={`0 0 ${LAYOUT.width} ${LAYOUT.height}`}
      width="100%"
      style={{ display: "block", maxWidth: LAYOUT.width, fontFamily: "inherit" }}
    >
      <FlowGraphSvg markerId="hookway-diagram-edge" />
    </svg>
  );
}

/** Shared graph used by the section diagram and the hero builder frame. */
export function FlowGraphSvg({ selectedId, markerId = "hookway-edge", animate = false }: { selectedId?: string; markerId?: string; animate?: boolean }) {
  const nodeById = (id: string) => FLOW_NODES.find((n) => n.id === id)!;
  return (
    <>
      <defs>
        <marker id={markerId} markerWidth="10" markerHeight="10" viewBox="0 0 10 10" refX="8" refY="5" markerUnits="userSpaceOnUse" orient="auto">
          <polyline points="3 2, 8 5, 3 8" fill="none" stroke="#8998AA" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
        </marker>
        <filter id={`${markerId}-lift`} x="-15%" y="-20%" width="130%" height="150%">
          <feDropShadow dx="0" dy="1" stdDeviation="1.6" floodColor={DARK.text} floodOpacity="0.08" />
        </filter>
      </defs>
      {FLOW_EDGES.map((e) => (
        <path
          key={`${e.from}-${e.to}`}
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          fill="none"
          stroke="#A5B2C2"
          strokeWidth={1.5}
          strokeLinecap="round"
          markerEnd={`url(#${markerId})`}
        />
      ))}
      {animate && FLOW_EDGES.map((e) => (
        <path
          key={`event-${e.from}-${e.to}`}
          className="hookway-flow-event"
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          pathLength={1}
          fill="none"
          stroke={DARK.accent}
          strokeWidth={3}
          strokeLinecap="round"
          strokeDasharray="0.07 1.93"
          aria-hidden="true"
          style={{ "--flow-delay": `${nodeById(e.from).column * 2.5}s` } as CSSProperties}
        />
      ))}
      {FLOW_NODES.map((n) => (
        <DiagramNode key={n.id} node={n} selected={n.id === selectedId} filterId={`${markerId}-lift`} />
      ))}
    </>
  );
}

function DiagramNode({ node, selected, filterId }: { node: FlowNode; selected?: boolean; filterId: string }) {
  const b = nodeBox(node);
  const chrome = nodeChrome(node);
  const r = LAYOUT.handle;
  const outColor = PORT[chrome.output] ?? EDGE.line;
  const color = NODE.border[node.kind];
  const tint = node.kind === "endpoint" ? "#EDF4FD" : node.kind === "filter" ? "#EDF6F0" : "#F0F2F6";
  return (
    <g filter={`url(#${filterId})`}>
      {selected && (
        <rect x={b.x - 4} y={b.y - 4} width={b.w + 8} height={b.h + 8} rx={12} fill="#E6EFFB" />
      )}
      <rect
        x={b.x} y={b.y} width={b.w} height={b.h} rx={8}
        fill={DARK.surface} stroke={selected ? DARK.accent : "#CFD7E2"}
        strokeWidth={selected ? 1.4 : 1}
      />
      <rect x={b.x + 12} y={b.y + 12} width={28} height={28} rx={7} fill={tint} />
      <svg x={b.x + 18} y={b.y + 18} width={16} height={16} viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        {node.kind === "endpoint" ? (
          <>
            <polyline points="8 5, 2 12, 8 19" />
            <polyline points="16 5, 22 12, 16 19" />
            <line x1={14} y1={4} x2={10} y2={20} />
          </>
        ) : node.kind === "filter" ? (
          <polygon points="3 4, 21 4, 14 12, 14 19, 10 21, 10 12" />
        ) : (
          <>
            <rect x={3} y={4} width={18} height={6} rx={2} />
            <rect x={3} y={14} width={18} height={6} rx={2} />
            <line x1={7} y1={7} x2={7.01} y2={7} />
            <line x1={7} y1={17} x2={7.01} y2={17} />
          </>
        )}
      </svg>
      <text x={b.x + 50} y={b.y + 23} fontSize={13} fontWeight={650} fill={DARK.text}>
        {node.yamlId}
      </text>
      <text x={b.x + 50} y={b.y + 38} fontSize={9.5} fill={DARK.muted}>
        {nodeLabel(node.nodeType)}
      </text>
      <rect x={b.x + 10} y={b.y + 50} width={b.w - 20} height={22} rx={4} fill="#F4F6F9" />
      <text x={b.x + 17} y={b.y + 64} fontSize={9.3} fill={DARK.muted} fontFamily={MONO}>
        {node.summary}
      </text>
      {chrome.showError && (
        <text x={b.x + b.w / 2} y={b.y + b.h - 7} fontSize={8} fill={PORT.on_error} textAnchor="middle">
          on error
        </text>
      )}
      {chrome.showIn && (
        <circle cx={b.x} cy={b.y + b.h / 2} r={r - 1} fill={DARK.surface} stroke={PORT[chrome.input] ?? EDGE.line} strokeWidth={1.6} />
      )}
      {chrome.showOut && (
        <circle cx={b.x + b.w} cy={b.y + b.h / 2} r={r - 1} fill={DARK.surface} stroke={outColor} strokeWidth={1.6} />
      )}
      {chrome.showError && (
        <circle cx={b.x + b.w / 2} cy={b.y + b.h} r={2.5} fill={DARK.surface} stroke={PORT.on_error} strokeWidth={1.2} />
      )}
    </g>
  );
}

function NodeCopy({ node }: { node: FlowNode }) {
  const chrome = nodeChrome(node);
  return (
    <>
      <Text size="sm" fw={600} c={DARK.text}>
        {node.yamlId}
      </Text>
      <Text size="xs" c={DARK.muted} style={{ fontFamily: MONO }}>
        {nodeLabel(node.nodeType)}
      </Text>
      <Text size="xs" c={DARK.muted}>
        {node.summary}
      </Text>
      {chrome.showError && (
        <Text size="xs" fw={600} c={PORT.on_error} mt={4} ta="center" style={{ fontSize: 10, letterSpacing: "0.03em" }}>
          on error
        </Text>
      )}
    </>
  );
}

// DiagramCards is the narrow-screen rendering: the same nodes stacked in flow
// order, so nothing needs horizontal scrolling.
function DiagramCards() {
  const order = ["ep", "f1", "sqs", "rabbit", "f2", "s3"];
  return (
    <Stack gap="xs" aria-label={flowDescription()} role="img">
      {order.map((id) => {
        const n = FLOW_NODES.find((x) => x.id === id)!;
        return (
          <Card
            key={n.id}
            radius="sm"
            padding="sm"
            ml={n.column * 16}
            style={{ background: DARK.surface, border: `1px solid ${DARK.border}` }}
          >
            <NodeCopy node={n} />
          </Card>
        );
      })}
    </Stack>
  );
}
