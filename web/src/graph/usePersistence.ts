import { useEffect } from "react";
import { useGraphStore } from "./store";
import { loadGraph, saveGraph } from "./persistence";
import { starterDocument } from "./starter";

const DEBOUNCE_MS = 400;

export function usePersistence(): void {
  const setGraph = useGraphStore((s) => s.setGraph);

  useEffect(() => {
    let cancelled = false;
    loadGraph().then((g) => {
      if (cancelled) return;
      if (g && (g.nodes.length > 0 || g.edges.length > 0 || Object.keys(g.otherFlows).length > 0)) {
        setGraph(g);
        return;
      }
      setGraph(starterDocument());
    });
    return () => {
      cancelled = true;
    };
  }, [setGraph]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsub = useGraphStore.subscribe((state) => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(
        () =>
          void saveGraph({
            nodes: state.nodes,
            edges: state.edges,
            serverConfig: state.serverConfig,
            configExtra: state.configExtra,
            connections: state.connections,
            destinations: state.destinations,
            activeFlow: state.activeFlow,
            otherFlows: state.otherFlows,
            deliveryFailurePolicy: state.deliveryFailurePolicy,
            flowExtra: state.flowExtra,
          }),
        DEBOUNCE_MS,
      );
    });
    return () => {
      if (timer) clearTimeout(timer);
      unsub();
    };
  }, []);
}
