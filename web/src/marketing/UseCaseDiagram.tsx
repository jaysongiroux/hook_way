import { useId, useLayoutEffect, useState } from "react";
import { useMediaQuery, useReducedMotion } from "@mantine/hooks";
import { useAnimate } from "motion/react-mini";
import { DARK } from "../tokens";
import type { ScenarioDiagram, ScenarioNode } from "./useCaseDiagrams";

export function UseCaseDiagram({ diagram }: { diagram: ScenarioDiagram }) {
  const smallViewport = useMediaQuery("(max-width: 48em)", false, {
    getInitialValueInEffect: false,
  });
  const reducedMotion = useReducedMotion(undefined, {
    getInitialValueInEffect: false,
  });
  const marker = useId();
  const [scope, animate] = useAnimate<SVGSVGElement>();
  const [canvasWidth, setCanvasWidth] = useState(0);
  const stages = Math.max(...diagram.nodes.map((node) => node.stage));
  const horizontalWidth = stages * 216 + 164 + 48;
  // The desktop rail also uses space: choose a layout from the canvas width,
  // keeping horizontal node labels at least 12px after SVG scaling.
  const narrow = canvasWidth
    ? canvasWidth < horizontalWidth * (12 / 13)
    : smallViewport;
  const nodeWidth = narrow ? 144 : 164;
  const nodeHeight = 64;
  const width = narrow ? 344 : horizontalWidth;
  const height = narrow ? stages * 104 + nodeHeight + 48 : 232;
  const position = (node: ScenarioNode) => ({
    x: narrow ? 20 + node.lane * 160 : 24 + node.stage * 216,
    y: narrow ? 24 + node.stage * 104 : 32 + node.lane * 104,
  });

  useLayoutEffect(() => {
    const canvas = scope.current.parentElement;
    if (!canvas) return;
    const measure = () => {
      const style = getComputedStyle(canvas);
      setCanvasWidth(
        canvas.clientWidth -
          parseFloat(style.paddingLeft) -
          parseFloat(style.paddingRight),
      );
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, [scope]);

  useLayoutEffect(() => {
    if (reducedMotion || typeof Element.prototype.animate !== "function")
      return;
    const nodes = Array.from(
      scope.current.querySelectorAll<SVGGElement>("[data-scenario-node]"),
    );
    const animations = nodes.map((element, index) =>
      animate(
        element,
        { opacity: [0.2, 1] },
        { duration: 0.55, delay: index * 0.07, ease: [0.22, 1, 0.36, 1] },
      ),
    );
    animations.push(
      animate(
        "[data-scenario-edge]",
        { strokeDashoffset: [1, 0] },
        { duration: 0.85, ease: [0.22, 1, 0.36, 1] },
      ),
    );
    return () => animations.forEach((animation) => animation.cancel());
  }, [diagram, narrow, reducedMotion, animate, scope]);

  return (
    <svg
      ref={scope}
      className="hookway-scenario-svg"
      data-orientation={narrow ? "vertical" : "horizontal"}
      role="img"
      aria-label={diagram.label}
      viewBox={`0 0 ${width} ${height}`}
    >
      <defs>
        <marker
          id={marker}
          markerWidth="8"
          markerHeight="8"
          refX="7"
          refY="4"
          orient="auto"
        >
          <path
            d="M1 1 L7 4 L1 7"
            fill="none"
            stroke={DARK.accent}
            strokeWidth="1.2"
          />
        </marker>
      </defs>
      {diagram.edges.map(([from, to]) => {
        const source = diagram.nodes.find((node) => node.id === from)!;
        const target = diagram.nodes.find((node) => node.id === to)!;
        const a = position(source),
          b = position(target);
        const x1 = narrow ? a.x + nodeWidth / 2 : a.x + nodeWidth;
        const y1 = narrow ? a.y + nodeHeight : a.y + nodeHeight / 2;
        const x2 = narrow ? b.x + nodeWidth / 2 : b.x;
        const y2 = narrow ? b.y : b.y + nodeHeight / 2;
        const path = narrow
          ? `M${x1} ${y1} C${x1} ${(y1 + y2) / 2}, ${x2} ${(y1 + y2) / 2}, ${x2} ${y2 - 4}`
          : `M${x1} ${y1} C${(x1 + x2) / 2} ${y1}, ${(x1 + x2) / 2} ${y2}, ${x2 - 4} ${y2}`;
        return (
          <path
            key={`${from}-${to}`}
            data-scenario-edge
            d={path}
            pathLength={1}
            strokeDasharray="1"
            fill="none"
            stroke={DARK.accent}
            strokeWidth="1.5"
            markerEnd={`url(#${marker})`}
          />
        );
      })}
      {diagram.nodes.map((node) => {
        const { x, y } = position(node);
        return (
          <g key={node.id} data-scenario-node>
            <rect
              x={x}
              y={y}
              width={nodeWidth}
              height={nodeHeight}
              rx="4"
              fill={DARK.surface}
              stroke={DARK.border}
            />
            <text
              x={x + 12}
              y={y + 26}
              fill={DARK.text}
              fontSize={narrow ? 12 : 13}
              fontWeight="600"
            >
              {node.label}
            </text>
            <text
              x={x + 12}
              y={y + 46}
              fill={DARK.muted}
              fontSize={narrow ? 10 : 11}
            >
              {node.detail}
            </text>
          </g>
        );
      })}
    </svg>
  );
}
