import { createContext, useContext } from "react";

/** Per-view presentation data. Graph renderers never access the editor store. */
export interface GraphPresentation {
  destinations: Record<string, unknown>;
  selectedNodeId: string | null;
  selectedEdgeId: string | null;
  onEdgeSelect?: (id: string) => void;
  readOnly: boolean;
}

export const GraphPresentationContext = createContext<GraphPresentation>({
  destinations: {},
  selectedNodeId: null,
  selectedEdgeId: null,
  readOnly: false,
});

export const useGraphPresentation = () => useContext(GraphPresentationContext);
