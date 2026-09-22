import { Card, Stack, Text } from "@mantine/core";
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
import { DARK, EDGE, PORT } from "../tokens";

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
      lead="Put conditions on individual branches; one can stop without blocking delivery on the others."
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
export function FlowGraphSvg({ selectedId, markerId = "hookway-edge" }: { selectedId?: string; markerId?: string }) {
  const nodeById = (id: string) => FLOW_NODES.find((n) => n.id === id)!;
  return (
    <>
      <defs>
        <marker id={markerId} markerWidth="10" markerHeight="8" refX="9" refY="4" orient="auto">
          <polygon points="0 0, 10 4, 0 8" fill={EDGE.line} />
        </marker>
      </defs>
      {FLOW_EDGES.map((e) => (
        <path
          key={`${e.from}-${e.to}`}
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          fill="none"
          stroke={EDGE.line}
          strokeWidth={1.5}
          markerEnd={`url(#${markerId})`}
        />
      ))}
      {FLOW_NODES.map((n) => (
        <DiagramNode key={n.id} node={n} selected={n.id === selectedId} />
      ))}
    </>
  );
}

function DiagramNode({ node, selected }: { node: FlowNode; selected?: boolean }) {
  const b = nodeBox(node);
  const chrome = nodeChrome(node);
  const r = LAYOUT.handle;
  const outColor = PORT[chrome.output] ?? EDGE.line;
  return (
    <g>
      {selected && (
        <rect
          x={b.x - 4}
          y={b.y - 4}
          width={b.w + 8}
          height={b.h + 8}
          rx={6}
          fill="none"
          stroke={outColor}
          strokeWidth={2}
        />
      )}
      <rect x={b.x} y={b.y} width={b.w} height={b.h} rx={4} fill={DARK.surface} stroke={DARK.border} strokeWidth={1} />
      <text x={b.x + 12} y={b.y + 22} fontSize={13} fontWeight={600} fill={DARK.text}>
        {node.yamlId}
      </text>
      <text x={b.x + 12} y={b.y + 40} fontSize={11} fill={DARK.muted} fontFamily={MONO}>
        {nodeLabel(node.nodeType)}
      </text>
      <text x={b.x + 12} y={b.y + 56} fontSize={11} fill={DARK.muted}>
        {node.summary}
      </text>
      {chrome.showError && (
        <text
          x={b.x + b.w / 2}
          y={b.y + b.h - 12}
          fontSize={10}
          fontWeight={600}
          fill={PORT.on_error}
          textAnchor="middle"
          letterSpacing="0.03em"
        >
          on error
        </text>
      )}
      {chrome.showIn && <circle cx={b.x} cy={b.y + b.h / 2} r={r} fill={PORT[chrome.input] ?? EDGE.line} />}
      {chrome.showOut && <circle cx={b.x + b.w} cy={b.y + b.h / 2} r={r} fill={outColor} />}
      {chrome.showError && <circle cx={b.x + b.w / 2} cy={b.y + b.h} r={r} fill={PORT.on_error} />}
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
