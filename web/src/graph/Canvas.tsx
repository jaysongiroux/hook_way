import { useCallback, useEffect, useMemo } from "react";
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  Panel,
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
import { DND_MIME, DND_TEMPLATE_MIME } from "./Palette";
import { isTemplateId, templateSubgraph } from "./templates";
import { useGraphStore } from "./store";
import { Button, Text } from "@mantine/core";
import { DARK, EDGE } from "../tokens";

/** React Flow stops at 0.5 by default, which cuts off zoom-out on a wide flow. */
const MIN_ZOOM = 0.1;

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

function CanvasInner({ onNodeActivate }: { onNodeActivate?: () => void }) {
  const { screenToFlowPosition, fitView } = useReactFlow();
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const arrangeSeq = useGraphStore((s) => s.arrangeSeq);
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const onNodesChange = useGraphStore((s) => s.onNodesChange);
  const onEdgesChange = useGraphStore((s) => s.onEdgesChange);
  const onConnect = useGraphStore((s) => s.onConnect);
  const addNode = useGraphStore((s) => s.addNode);
  const insertSubgraph = useGraphStore((s) => s.insertSubgraph);
  const setSelected = useGraphStore((s) => s.setSelected);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);

  const routedEdges = useMemo(() => {
    const groups = parallelGroups(edges);
    return edges.map((e) => {
      const sel = e.id === selectedEdgeId;
      const markerEnd = {
        type: MarkerType.ArrowClosed,
        width: EDGE.arrowSize,
        height: EDGE.arrowSize,
        markerUnits: "userSpaceOnUse",
        color: sel ? EDGE.selected : EDGE.line,
      };
      const style = sel
        ? { stroke: EDGE.selected, strokeWidth: EDGE.selectedWidth }
        : { stroke: EDGE.line, strokeWidth: EDGE.width };
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

  useEffect(() => {
    if (arrangeSeq === 0) return;
    const id = requestAnimationFrame(() => fitView({ padding: 0.2, duration: 200, minZoom: MIN_ZOOM }));
    return () => cancelAnimationFrame(id);
  }, [arrangeSeq, fitView]);

  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      const position = screenToFlowPosition({ x: e.clientX, y: e.clientY });
      // Templates are checked first: a template drag carries only its own mime
      // type, so a stray node-mime read would be empty and silently do nothing.
      const templateId = e.dataTransfer.getData(DND_TEMPLATE_MIME);
      if (isTemplateId(templateId)) {
        const sub = templateSubgraph(templateId);
        if (sub) insertSubgraph(sub, position);
        return;
      }
      const kind = e.dataTransfer.getData(DND_MIME);
      if (!isNodeKind(kind)) return;
      addNode(createNode(kind, position));
    },
    [screenToFlowPosition, addNode, insertSubgraph],
  );

  return (
    <div style={{ width: "100%", height: "100%" }} onDrop={onDrop} onDragOver={onDragOver}>
      <ReactFlow
        key={activeFlow}
        colorMode="light"
        style={{ background: DARK.bg }}
        nodes={nodes}
        edges={routedEdges}
        minZoom={MIN_ZOOM}
        fitViewOptions={{ padding: 0.2, minZoom: MIN_ZOOM }}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        isValidConnection={validate}
        onNodeClick={(_, node: Node) => {
          setSelected(node.id);
          // Only nodes announce activation. An edge's inspector view is a
          // single informational line, so popping a modal for every edge
          // click would be noise -- the header button still reaches it.
          onNodeActivate?.();
        }}
        onEdgeClick={(_, edge) => setSelectedEdge(edge.id)}
        onPaneClick={() => setSelected(null)}
        fitView
      >
        <Background color={DARK.border} gap={18} size={1} />
        <Controls
          style={{
            background: DARK.surface,
            border: `1px solid ${DARK.border}`,
            borderRadius: 4,
            overflow: "hidden",
          }}
        />
        <Panel position="bottom-left">
          <Button
            size="xs"
            variant="default"
            aria-label="Arrange nodes"
            disabled={nodes.length === 0}
            onClick={() => useGraphStore.getState().arrangeNodes()}
            styles={{
              root: {
                marginLeft: 44,
                background: DARK.surface,
                borderColor: DARK.border,
              },
            }}
          >
            Arrange
          </Button>
        </Panel>
        {nodes.length === 0 && (
          <Panel position="top-center">
            <Text
              size="sm"
              c={DARK.muted}
              px="md"
              py={8}
              style={{
                background: DARK.surface,
                border: `1px solid ${DARK.border}`,
                borderRadius: 4,
              }}
            >
              Open Nodes to add a node, or start with a template.
            </Text>
          </Panel>
        )}
      </ReactFlow>
    </div>
  );
}

export function Canvas({ onNodeActivate }: { onNodeActivate?: () => void }) {
  return (
    <ReactFlowProvider>
      <CanvasInner onNodeActivate={onNodeActivate} />
    </ReactFlowProvider>
  );
}
