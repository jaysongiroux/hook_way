import { useCallback, useMemo } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MarkerType,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { nodeTypes } from "./nodes";
import { edgeTypes } from "./RouteEdge";
import { isValidConnection } from "./connection";
import { createNode, isNodeKind } from "./model";
import { DND_MIME } from "./Palette";
import { useGraphStore } from "./store";
import { DARK, EDGE } from "../tokens";

function parallelGroups(edges: Edge[]): Map<string, { index: number; count: number }> {
  const byKey = new Map<string, string[]>();
  for (const e of edges) {
    const key = `${e.source}:${e.sourceHandle ?? ""}->${e.target}`;
    const ids = byKey.get(key);
    if (ids) ids.push(e.id);
    else byKey.set(key, [e.id]);
  }
  const out = new Map<string, { index: number; count: number }>();
  for (const ids of byKey.values()) {
    ids.forEach((id, index) => out.set(id, { index, count: ids.length }));
  }
  return out;
}

function CanvasInner() {
  const { screenToFlowPosition } = useReactFlow();
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const onNodesChange = useGraphStore((s) => s.onNodesChange);
  const onEdgesChange = useGraphStore((s) => s.onEdgesChange);
  const onConnect = useGraphStore((s) => s.onConnect);
  const addNode = useGraphStore((s) => s.addNode);
  const setSelected = useGraphStore((s) => s.setSelected);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);

  const routedEdges = useMemo(() => {
    const groups = parallelGroups(edges);
    return edges.map((e) => {
      const sel = e.id === selectedEdgeId;
      const markerEnd = {
        type: MarkerType.ArrowClosed,
        width: 16,
        height: 16,
        markerUnits: "userSpaceOnUse",
        color: sel ? EDGE.selected : EDGE.line,
      };
      const style = sel ? { stroke: EDGE.selected, strokeWidth: 2.5 } : { stroke: EDGE.line, strokeWidth: 1.5 };
      const g = groups.get(e.id);
      const port = e.sourceHandle && e.sourceHandle !== "out" ? String(e.sourceHandle) : "";
      return {
        ...e,
        type: "route",
        markerEnd,
        style,
        data: { ...(e.data ?? {}), label: port, parallelIndex: g?.index ?? 0, parallelCount: g?.count ?? 1 },
      };
    });
  }, [edges, selectedEdgeId]);

  const validate = useCallback((c: Connection | Edge) => isValidConnection(c, nodes, edges), [nodes, edges]);

  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      const kind = e.dataTransfer.getData(DND_MIME);
      if (!isNodeKind(kind)) return;
      const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
      addNode(createNode(kind, position));
    },
    [screenToFlowPosition, addNode],
  );

  return (
    <div style={{ width: "100%", height: "100%" }} onDrop={onDrop} onDragOver={onDragOver}>
      <ReactFlow
        colorMode="dark"
        style={{ background: DARK.bg }}
        nodes={nodes}
        edges={routedEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        isValidConnection={validate}
        onNodeClick={(_, node: Node) => setSelected(node.id)}
        onEdgeClick={(_, edge) => setSelectedEdge(edge.id)}
        onPaneClick={() => setSelected(null)}
        fitView
      >
        <Background color="#2A3A57" />
        <Controls />
      </ReactFlow>
    </div>
  );
}

export function Canvas() {
  return (
    <ReactFlowProvider>
      <CanvasInner />
    </ReactFlowProvider>
  );
}
