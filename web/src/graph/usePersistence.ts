import { useEffect } from "react";
import { useGraphStore } from "./store";
import { loadGraph, saveGraph } from "./persistence";
import { starterDocument } from "./starter";
import type { GraphDocument } from "./model";

const DEBOUNCE_MS = 400;
const DOCUMENT_FIELDS: (keyof GraphDocument)[] = [
  "nodes", "edges", "serverConfig", "coreReplayConfig", "replayServiceConfig",
  "configExtra", "connections", "destinations", "activeFlow", "otherFlows",
  "deliveryFailurePolicy", "flowExtra",
];

export function usePersistence(): void {
  const setGraph = useGraphStore((s) => s.setGraph);

  useEffect(() => {
    let cancelled = false;
    let hydrated = false;
    let changedDuringLoad = false;
    let previousDocument = useGraphStore.getState().document();
    let timer: ReturnType<typeof setTimeout> | undefined;

    const scheduleSave = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => void saveGraph(useGraphStore.getState().document()), DEBOUNCE_MS);
    };

    const unsub = useGraphStore.subscribe((state) => {
      const document = state.document();
      const changed = DOCUMENT_FIELDS.some((field) => document[field] !== previousDocument[field]);
      previousDocument = document;
      if (!changed) return;
      if (!hydrated) changedDuringLoad = true;
      else scheduleSave();
    });

    void loadGraph().then((graph) => {
      if (cancelled) return;
      hydrated = true;
      if (changedDuringLoad) {
        scheduleSave();
      } else {
        setGraph(graph ?? starterDocument());
      }
    });

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      unsub();
    };
  }, [setGraph]);
}
