import { useCallback, useMemo } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { nodeTypes } from "./nodes";
import { edgeTypes } from "./RouteEdge";
import { isValidConnection } from "./connection";
import { parallelGroups } from "./routes";
import { createNode, isNodeKind } from "./model";
import { DND_MIME } from "./Palette";
import { useGraphStore } from "./store";

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

  const routedEdges = useMemo(() => {
    const destIds = new Set(nodes.filter((n) => n.data.kind === "destination").map((n) => n.id));
    const groups = parallelGroups(edges);
    return edges.map((e) => {
      if (!destIds.has(e.target)) return e;
      const g = groups.get(e.id);
      return {
        ...e,
        type: "route",
        data: { ...(e.data ?? {}), parallelIndex: g?.index ?? 0, parallelCount: g?.count ?? 1 },
      };
    });
  }, [nodes, edges]);

  const validate = useCallback(
    (c: Connection | Edge) => isValidConnection(c, nodes, edges),
    [nodes, edges],
  );

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
        <Background />
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
