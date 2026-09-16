import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps, type EdgeTypes } from "@xyflow/react";
import { useGraphStore } from "./store";
import { DARK, EDGE } from "../tokens";

const PARALLEL_GAP = 32;

export function RouteEdge({
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

  let path: string;
  let labelX: number;
  let labelY: number;
  if (count > 1) {
    const dx = targetX - sourceX;
    const dy = targetY - sourceY;
    const len = Math.hypot(dx, dy) || 1;
    const nx = -dy / len;
    const ny = dx / len;
    const offset = (index - (count - 1) / 2) * PARALLEL_GAP;
    const cx = (sourceX + targetX) / 2 + nx * offset;
    const cy = (sourceY + targetY) / 2 + ny * offset;
    path = `M ${sourceX},${sourceY} Q ${cx},${cy} ${targetX},${targetY}`;
    labelX = 0.25 * sourceX + 0.5 * cx + 0.25 * targetX;
    labelY = 0.25 * sourceY + 0.5 * cy + 0.25 * targetY;
  } else {
    [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
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
              background: selected ? "#24365C" : DARK.surface,
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
