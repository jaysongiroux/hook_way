import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent, type DragEventHandler } from "react";
import {
  ReactFlow, ReactFlowProvider, Background, Controls, Panel, MarkerType,
  useReactFlow, useNodesInitialized, type Edge, type ReactFlowProps,
} from "@xyflow/react";
import { Button } from "@mantine/core";
import "@xyflow/react/dist/style.css";
import "./graphView.css";
import { nodeTypes } from "./nodes";
import { edgeTypes } from "./RouteEdge";
import { GraphPresentationContext } from "./GraphPresentation";
import { layout } from "./layout";
import { yamlIdOf, type FlowSnapshot, type HookwayNode } from "./model";
import { nodeLabel } from "./nodeSchema";
import { DARK, EDGE } from "../tokens";

const MIN_ZOOM = 0.1;
const FIT_OPTIONS = { padding: 0.2, minZoom: MIN_ZOOM };

interface ViewProps {
  graph: FlowSnapshot;
  destinations: Record<string, unknown>;
  selectedNodeId: string | null;
  selectedEdgeId: string | null;
  onNodeSelect: (id: string | null) => void;
  onNodeActivate?: (id: string) => void;
  onEdgeSelect: (id: string) => void;
}

type Editing = Required<Pick<ReactFlowProps<HookwayNode>,
  "onNodesChange" | "onEdgesChange" | "onConnect" | "isValidConnection"
>> & {
  onDrop: (event: DragEvent<HTMLDivElement>, position: { x: number; y: number }) => void;
  onDragOver: DragEventHandler<HTMLDivElement>;
  onArrange: () => void;
  arrangeSeq: number;
};

export type GraphViewProps = ViewProps & (
  | { mode: "edit"; editing: Editing }
  | { mode: "readOnly"; editing?: never }
);

function parallelGroups(edges: Edge[]): Map<string, { index: number; count: number }> {
  const byKey = new Map<string, string[]>();
  for (const edge of edges) {
    const key = JSON.stringify([edge.source, edge.sourceHandle ?? "", edge.target]);
    const ids = byKey.get(key);
    if (ids) ids.push(edge.id);
    else byKey.set(key, [edge.id]);
  }
  const groups = new Map<string, { index: number; count: number }>();
  for (const ids of byKey.values())
    ids.forEach((id, index) => groups.set(id, { index, count: ids.length }));
  return groups;
}

function GraphViewInner(props: GraphViewProps) {
  const { graph, destinations, selectedNodeId, selectedEdgeId, onNodeSelect, onNodeActivate, onEdgeSelect } = props;
  const readOnly = props.mode === "readOnly";
  const editing = props.editing;
  const { getNodes, fitView, screenToFlowPosition } = useReactFlow<HookwayNode>();
  const initialized = useNodesInitialized();
  const root = useRef<HTMLDivElement>(null);
  const arrangedFor = useRef<HookwayNode[] | null>(null);
  const [measured, setMeasured] = useState<{ source: HookwayNode[]; nodes: HookwayNode[] }>();
  const displayedNodes = readOnly && measured?.source === graph.nodes ? measured.nodes : graph.nodes;
  const nodes = useMemo(() => readOnly ? displayedNodes.map((node) => ({
    ...node,
    draggable: false, connectable: false, deletable: false,
    ariaRole: "button" as const,
    ariaLabel: yamlIdOf(node) + " (" + nodeLabel(node.data.nodeType) + ") — view details",
  })) : displayedNodes, [displayedNodes, readOnly]);

  const routedEdges = useMemo(() => {
    const groups = parallelGroups(graph.edges);
    return graph.edges.map((edge) => {
      const selected = edge.id === selectedEdgeId;
      const group = groups.get(edge.id);
      const port = edge.sourceHandle && edge.sourceHandle !== "out" ? String(edge.sourceHandle) : "";
      return {
        ...edge,
        ...(readOnly ? { reconnectable: false, deletable: false } : {}),
        type: "route",
        markerEnd: {
          type: MarkerType.ArrowClosed, width: EDGE.arrowSize, height: EDGE.arrowSize,
          markerUnits: "userSpaceOnUse", color: selected ? EDGE.selected : EDGE.line,
        },
        style: { stroke: selected ? EDGE.selected : EDGE.line, strokeWidth: selected ? EDGE.selectedWidth : EDGE.width },
        data: { ...edge.data, label: port, parallelIndex: group?.index ?? 0, parallelCount: group?.count ?? 1 },
      };
    });
  }, [graph.edges, selectedEdgeId, readOnly]);

  // Import uses estimated card sizes; preview layout gets one measured pass so
  // descriptions and nodes with many branch ports cannot overlap their neighbours.
  useEffect(() => {
    if (!readOnly || !initialized || arrangedFor.current === graph.nodes) return;
    arrangedFor.current = graph.nodes;
    setMeasured({ source: graph.nodes, nodes: layout(getNodes(), graph.edges) });
  }, [readOnly, initialized, graph.nodes, graph.edges, getNodes]);

  useEffect(() => {
    if (!readOnly || measured?.source !== graph.nodes) return;
    const frame = requestAnimationFrame(() => void fitView(FIT_OPTIONS));
    return () => cancelAnimationFrame(frame);
  }, [readOnly, measured, graph.nodes, fitView]);

  useEffect(() => {
    if (!readOnly || !root.current) return;
    let frame = 0;
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => void fitView(FIT_OPTIONS));
    });
    observer.observe(root.current);
    return () => { observer.disconnect(); cancelAnimationFrame(frame); };
  }, [readOnly, fitView]);

  useEffect(() => {
    if (!editing?.arrangeSeq) return;
    const frame = requestAnimationFrame(() => void fitView({ ...FIT_OPTIONS, duration: 200 }));
    return () => cancelAnimationFrame(frame);
  }, [editing?.arrangeSeq, fitView]);

  const activate = useCallback((id: string) => {
    onNodeSelect(id);
    onNodeActivate?.(id);
  }, [onNodeSelect, onNodeActivate]);

  return (
    <GraphPresentationContext value={{ destinations, selectedNodeId, selectedEdgeId, onEdgeSelect, readOnly }}>
      <div
        ref={root}
        className="hookway-graph-view"
        data-mode={props.mode}
        style={{ color: DARK.text, "--graph-surface": DARK.surface, "--graph-border": DARK.border, "--graph-recessed": DARK.recessed, "--graph-muted": DARK.muted } as CSSProperties}
        onDrop={editing ? (event) => editing.onDrop(event, screenToFlowPosition({ x: event.clientX, y: event.clientY })) : undefined}
        onDragOver={editing?.onDragOver}
        onKeyDownCapture={readOnly ? (event) => {
          if (event.key !== "Enter" && event.key !== " ") return;
          const node = (event.target as HTMLElement).closest<HTMLElement>(".react-flow__node");
          if (!node?.dataset.id) return;
          event.preventDefault();
          event.stopPropagation();
          activate(node.dataset.id);
        } : undefined}
      >
        <ReactFlow<HookwayNode>
          colorMode="light"
          style={{ background: DARK.bg }}
          nodes={nodes}
          edges={routedEdges}
          minZoom={MIN_ZOOM}
          fitViewOptions={FIT_OPTIONS}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={editing?.onNodesChange}
          onEdgesChange={editing?.onEdgesChange}
          onConnect={editing?.onConnect}
          isValidConnection={editing?.isValidConnection}
          onNodeClick={(_, node) => readOnly ? activate(node.id) : onNodeSelect(node.id)}
          onNodeDoubleClick={readOnly ? undefined : (_, node) => activate(node.id)}
          onEdgeClick={(_, edge) => onEdgeSelect(edge.id)}
          onPaneClick={() => onNodeSelect(null)}
          nodesDraggable={!readOnly}
          nodesConnectable={!readOnly}
          connectOnClick={!readOnly}
          edgesReconnectable={!readOnly}
          edgesFocusable={!readOnly}
          deleteKeyCode={readOnly ? null : undefined}
          selectionKeyCode={readOnly ? null : undefined}
          multiSelectionKeyCode={readOnly ? null : undefined}
          panOnScroll={readOnly}
          zoomOnScroll={!readOnly}
          zoomOnDoubleClick={!readOnly}
          ariaLabelConfig={readOnly ? {
            "node.a11yDescription.default": "Press Enter or Space to view node details.",
            "node.a11yDescription.keyboardDisabled": "Press Enter or Space to view node details.",
          } : undefined}
          fitView
        >
          <Background color={DARK.border} gap={18} size={1} />
          <Controls showInteractive={!readOnly} fitViewOptions={FIT_OPTIONS}
            style={{ background: DARK.surface, border: "1px solid " + DARK.border, borderRadius: 4, overflow: "hidden" }} />
          {editing && (
            <Panel position="bottom-left">
              <Button size="xs" variant="default" aria-label="Arrange nodes" disabled={!nodes.length} onClick={editing.onArrange}
                styles={{ root: { marginLeft: 44, background: DARK.surface, borderColor: DARK.border } }}>
                Arrange
              </Button>
            </Panel>
          )}
        </ReactFlow>
      </div>
    </GraphPresentationContext>
  );
}

export function GraphView(props: GraphViewProps) {
  return <ReactFlowProvider><GraphViewInner {...props} /></ReactFlowProvider>;
}
