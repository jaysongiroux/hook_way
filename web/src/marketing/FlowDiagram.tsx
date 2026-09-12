import { Anchor, Box, Card, Group, Stack, Text } from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { Link } from "react-router-dom";
import { FLOW_EDGES, FLOW_NODES, LAYOUT, edgePath, flowDescription, nodeBox, type FlowNode } from "./flowGraph";
import { Section } from "./Section";
import { DARK } from "../tokens";

const BORDER = DARK.border;
const TEXT = DARK.text;

// The pulse is a short dash travelling each edge, which reads as an event
// moving through the graph. Driven by CSS rather than SMIL so that
// prefers-reduced-motion can switch it off, which a media query cannot do to
// an SVG <animate> element.
const PULSE_CSS = `
@keyframes hookway-pulse {
  from { stroke-dashoffset: 240; }
  to { stroke-dashoffset: 0; }
}
.hookway-pulse {
  stroke-dasharray: 14 226;
  animation: hookway-pulse 3s linear infinite;
}
@media (prefers-reduced-motion: reduce) {
  .hookway-pulse { animation: none; opacity: 0; }
}
`;

// FlowDiagram shows one verified endpoint fanning out to three different
// backends, with route-level filters gating two of the branches. Drawn as
// inline SVG rather than embedding the builder's canvas, so the overview does
// not pay for React Flow.
export function FlowDiagram() {
  // One variant renders at a time, so the labels appear once in the DOM and
  // once in the accessibility tree.
  const wide = useMediaQuery("(min-width: 48em)", true);
  return (
    <Section
      eyebrow="Routing"
      title="One webhook, every destination that needs it"
      lead="Give a route its own filter and only that branch drops when the condition fails, while the rest still deliver."
    >
      {wide ? <DiagramSvg /> : <DiagramCards />}
      <Group mt="xl">
        <Anchor component={Link} to="/builder" size="sm" fw={600} c={DARK.accentText}>
          Draw your own in the builder →
        </Anchor>
      </Group>
    </Section>
  );
}

function DiagramSvg() {
  const nodeById = (id: string) => FLOW_NODES.find((n) => n.id === id)!;
  return (
    <svg
      role="img"
      aria-label={flowDescription()}
      viewBox={`0 0 ${LAYOUT.width} ${LAYOUT.height}`}
      width="100%"
      style={{ display: "block", maxWidth: LAYOUT.width, fontFamily: "inherit" }}
    >
      <style>{PULSE_CSS}</style>
      {FLOW_EDGES.map((e) => (
        <path
          key={`${e.from}-${e.to}`}
          d={edgePath(nodeById(e.from), nodeById(e.to))}
          fill="none"
          stroke="#3A4D70"
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
        <SvgNode key={n.id} node={n} />
      ))}
    </svg>
  );
}

function SvgNode({ node }: { node: FlowNode }) {
  const b = nodeBox(node);
  return (
    <g>
      <rect x={b.x} y={b.y} width={b.w} height={b.h} rx={8} fill={DARK.surface} stroke={BORDER} strokeWidth={1} />
      <rect x={b.x + 12} y={b.y + 12} width={24} height={3} rx={1.5} fill={node.accent} />
      <text x={b.x + 12} y={b.y + 32} fontSize={13} fontWeight={600} fill={TEXT}>
        {node.label}
      </text>
      <text x={b.x + 12} y={b.y + 48} fontSize={11} fill={DARK.muted}>
        {node.sub}
      </text>
    </g>
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
            radius="md"
            padding="sm"
            ml={n.column * 16}
            style={{ background: DARK.surface, border: `1px solid ${DARK.border}` }}
          >
            <Box style={{ width: 24, height: 3, borderRadius: 1.5, background: n.accent, marginBottom: 6 }} />
            <Text size="sm" fw={600} c={TEXT}>
              {n.label}
            </Text>
            <Text size="xs" c={DARK.muted}>
              {n.sub}
            </Text>
          </Card>
        );
      })}
    </Stack>
  );
}
