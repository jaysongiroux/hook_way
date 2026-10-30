import { useCallback } from "react";
import type { Connection, Edge } from "@xyflow/react";
import { GraphView } from "./GraphView";
import { isValidConnection } from "./connection";
import { createNode, isNodeKind } from "./model";
import { DND_MIME, DND_TEMPLATE_MIME } from "./Palette";
import { isTemplateId, templateSubgraph } from "./templates";
import { useGraphStore } from "./store";

/** The editable builder adapter; the shared view owns rendering only. */
export function Canvas({ onNodeActivate }: { onNodeActivate?: () => void }) {
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const arrangeSeq = useGraphStore((s) => s.arrangeSeq);
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const destinations = useGraphStore((s) => s.destinations);
  const onNodesChange = useGraphStore((s) => s.onNodesChange);
  const onEdgesChange = useGraphStore((s) => s.onEdgesChange);
  const onConnect = useGraphStore((s) => s.onConnect);
  const addNode = useGraphStore((s) => s.addNode);
  const insertSubgraph = useGraphStore((s) => s.insertSubgraph);
  const setSelected = useGraphStore((s) => s.setSelected);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  const selectedId = useGraphStore((s) => s.selectedId);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);
  const arrangeNodes = useGraphStore((s) => s.arrangeNodes);
  const validate = useCallback((c: Connection | Edge) => isValidConnection(c, nodes, edges), [nodes, edges]);

  return (
    <GraphView
      key={activeFlow}
      mode="edit"
      graph={{ nodes, edges }}
      destinations={destinations}
      selectedNodeId={selectedId}
      selectedEdgeId={selectedEdgeId}
      onNodeSelect={setSelected}
      onEdgeSelect={setSelectedEdge}
      onNodeActivate={onNodeActivate}
      editing={{
        onNodesChange, onEdgesChange, onConnect, isValidConnection: validate,
        arrangeSeq, onArrange: arrangeNodes,
        onDragOver: (event) => {
          event.preventDefault();
          event.dataTransfer.dropEffect = "move";
        },
        onDrop: (event, position) => {
          event.preventDefault();
          const templateId = event.dataTransfer.getData(DND_TEMPLATE_MIME);
          if (isTemplateId(templateId)) {
            const sub = templateSubgraph(templateId);
            if (sub) insertSubgraph(sub, position);
            return;
          }
          const kind = event.dataTransfer.getData(DND_MIME);
          if (isNodeKind(kind)) addNode(createNode(kind, position));
        },
      }}
    />
  );
}
