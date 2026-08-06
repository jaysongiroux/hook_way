import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps, type EdgeTypes } from "@xyflow/react";
import { useGraphStore } from "./store";

const PARALLEL_GAP = 32;

export function RouteEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps) {
  const d = (data ?? {}) as { target?: string; parallelIndex?: number; parallelCount?: number };
  const count = d.parallelCount ?? 1;
  const index = d.parallelIndex ?? 0;
  const target = String(d.target ?? "");
  const selected = useGraphStore((s) => s.selectedEdgeId === id);

  let path: string;
  let labelX: number;
  let labelY: number;
  if (count > 1) {
    // Bow each parallel edge apart by a perpendicular offset so same source→target
    // routes (and their chips) don't stack on one line.
    const dx = targetX - sourceX;
    const dy = targetY - sourceY;
    const len = Math.hypot(dx, dy) || 1;
    const nx = -dy / len;
    const ny = dx / len;
    const offset = (index - (count - 1) / 2) * PARALLEL_GAP;
    const cx = (sourceX + targetX) / 2 + nx * offset;
    const cy = (sourceY + targetY) / 2 + ny * offset;
    path = `M ${sourceX},${sourceY} Q ${cx},${cy} ${targetX},${targetY}`;
    // quadratic point at t=0.5
    labelX = 0.25 * sourceX + 0.5 * cx + 0.25 * targetX;
    labelY = 0.25 * sourceY + 0.5 * cy + 0.25 * targetY;
  } else {
    [path, labelX, labelY] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
  }

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        style={selected ? { stroke: "#366CED", strokeWidth: 2.5 } : undefined}
      />
      {target && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: "absolute",
              transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
              background: selected ? "#eaf0fe" : "#fff",
              border: `1px solid ${selected ? "#366CED" : "#d6def0"}`,
              borderRadius: 6,
              padding: "1px 6px",
              fontSize: 11,
              fontWeight: selected ? 600 : 400,
              color: "#152033",
              pointerEvents: "all",
              cursor: "pointer",
            }}
            onClick={(e) => {
              e.stopPropagation();
              useGraphStore.getState().setSelectedEdge(id);
            }}
          >
            {target}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

export const edgeTypes: EdgeTypes = { route: RouteEdge };
