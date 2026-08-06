import { useEffect } from "react";
import { useGraphStore } from "./store";
import { loadGraph, saveGraph } from "./persistence";

const DEBOUNCE_MS = 400;

export function usePersistence(): void {
  const setGraph = useGraphStore((s) => s.setGraph);

  // Rehydrate once on mount.
  useEffect(() => {
    let cancelled = false;
    loadGraph().then((g) => {
      if (!cancelled && g && (g.nodes.length > 0 || g.edges.length > 0)) {
        setGraph(g.nodes, g.edges, g.serverConfig, g.configExtra);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [setGraph]);

  // Debounced autosave on graph changes.
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsub = useGraphStore.subscribe((state) => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => void saveGraph(state.nodes, state.edges, state.serverConfig, state.configExtra), DEBOUNCE_MS);
    });
    return () => {
      if (timer) clearTimeout(timer);
      unsub();
    };
  }, []);
}
