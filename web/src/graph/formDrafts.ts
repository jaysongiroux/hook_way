import { createContext, useContext, useState } from "react";
import { createStore, useStore, type StoreApi } from "zustand";
import type { EditorDraft } from "./model";
import { reconcileConfigExtraDrafts, useGraphStore } from "./store";

type DraftState = {
  formDrafts?: Record<string, EditorDraft>;
  setFormDraft: (key: string, draft: EditorDraft | undefined) => void;
};
export const FieldDraftContext = createContext<StoreApi<DraftState> | null>(
  null,
);
export function createDraftStore() {
  return createStore<DraftState>((set) => ({
    formDrafts: {},
    setFormDraft: (key, draft) =>
      set((s) => {
        const next = { ...s.formDrafts };
        if (draft) next[key] = draft;
        else delete next[key];
        return { formDrafts: next };
      }),
  }));
}
export function useClearDraftBranch() {
  const store = useContext(FieldDraftContext) ?? useGraphStore;
  return (scope: string | undefined, path = "") =>
    clearDraftBranch(scope, path, store);
}

export function draftKey(scope: string, path: string): string {
  return `${scope}|${path}`;
}

/** Invalid edits belong to the document, not to a modal's mount lifetime. */
export function useFieldDraft(scope: string | undefined, path: string) {
  const key = scope ? draftKey(scope, path) : "";
  const store = useContext(FieldDraftContext) ?? useGraphStore;
  const saved = useStore(store, (s) => (key ? s.formDrafts?.[key] : undefined));
  const write = useStore(store, (s) => s.setFormDraft);
  const [local, setLocal] = useState<EditorDraft>();
  return [
    scope ? saved : local,
    (draft: EditorDraft | undefined) =>
      scope ? write(key, draft) : setLocal(draft),
  ] as const;
}

export function clearDraftBranch(
  scope: string | undefined,
  path = "",
  owner: StoreApi<DraftState> = useGraphStore,
) {
  if (!scope) return;
  const store = owner.getState();
  for (const key of Object.keys(store.formDrafts ?? {})) {
    const divider = key.indexOf("|");
    const entryScope = key.slice(0, divider),
      entryPath = key.slice(divider + 1);
    if (
      (entryScope === scope || entryScope.startsWith(`${scope}/`)) &&
      (!path || entryPath === path || entryPath.startsWith(`${path}.`))
    )
      store.setFormDraft(key, undefined);
  }
}

export function nodeScope(flow: string, id: string) {
  return `node/${encodeURIComponent(flow)}/${encodeURIComponent(id)}`;
}

/** Credential editors may be hosted by the wizard's independent draft store. */
export function useReconcileCredentialDrafts() {
  const owner = useContext(FieldDraftContext);
  return (before: unknown, after: unknown) => {
    if (!owner) return; // The graph store already reconciles its own committed edits.
    const state = owner.getState();
    const current = state.formDrafts ?? {};
    const next = reconcileConfigExtraDrafts(
      current,
      { credentials: before },
      { credentials: after },
    );
    for (const key of new Set([
      ...Object.keys(current),
      ...Object.keys(next),
    ])) {
      if (current[key] !== next[key]) state.setFormDraft(key, next[key]);
    }
  };
}
