import { BaseEdge, EdgeLabelRenderer, getBezierPath, Position, type EdgeProps, type EdgeTypes } from "@xyflow/react";
import { useGraphStore } from "./store";
import { DARK, EDGE } from "../tokens";

const PARALLEL_GAP = 32;

/**
 * backOff pulls the path's end point away from the target handle along the side
 * it enters from. React Flow runs an edge from handle centre to handle centre,
 * so the arrowhead would otherwise be drawn underneath the target's handle dot
 * and never be seen. Stopping short puts the arrow in open canvas.
 */
function backOff(x: number, y: number, side: Position): { x: number; y: number } {
  switch (side) {
    case Position.Left:
      return { x: x - EDGE.arrowGap, y };
    case Position.Right:
      return { x: x + EDGE.arrowGap, y };
    case Position.Top:
      return { x, y: y - EDGE.arrowGap };
    default:
      return { x, y: y + EDGE.arrowGap };
  }
}

function RouteEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  data,
}: EdgeProps) {
  const d = (data ?? {}) as { label?: string; parallelIndex?: number; parallelCount?: number };
  const count = d.parallelCount ?? 1;
  const index = d.parallelIndex ?? 0;
  const label = String(d.label ?? "");
  const selected = useGraphStore((s) => s.selectedEdgeId === id);

  const end = backOff(targetX, targetY, targetPosition);

  let path: string;
  let labelX: number;
  let labelY: number;
  if (count > 1) {
    const dx = end.x - sourceX;
    const dy = end.y - sourceY;
    const len = Math.hypot(dx, dy) || 1;
    const nx = -dy / len;
    const ny = dx / len;
    const offset = (index - (count - 1) / 2) * PARALLEL_GAP;
    const cx = (sourceX + end.x) / 2 + nx * offset;
    const cy = (sourceY + end.y) / 2 + ny * offset;
    path = `M ${sourceX},${sourceY} Q ${cx},${cy} ${end.x},${end.y}`;
    labelX = 0.25 * sourceX + 0.5 * cx + 0.25 * end.x;
    labelY = 0.25 * sourceY + 0.5 * cy + 0.25 * end.y;
  } else {
    [path, labelX, labelY] = getBezierPath({
      sourceX,
      sourceY,
      targetX: end.x,
      targetY: end.y,
      sourcePosition,
      targetPosition,
    });
  }

  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} style={style} />
      {label && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              background: selected ? DARK.recessed : DARK.surface,
              border: `1px solid ${selected ? EDGE.selected : EDGE.labelBorder}`,
              borderRadius: 6,
              padding: "1px 6px",
              fontSize: 11,
              fontWeight: selected ? 600 : 400,
              color: DARK.text,
              pointerEvents: "all",
              cursor: "pointer",
              fontFamily: "ui-monospace, Menlo, monospace",
            }}
            onClick={(e) => {
              e.stopPropagation();
              useGraphStore.getState().setSelectedEdge(id);
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

export const edgeTypes: EdgeTypes = { route: RouteEdge };
