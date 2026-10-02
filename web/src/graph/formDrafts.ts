import { useState } from "react";
import type { EditorDraft } from "./model";
import { useGraphStore } from "./store";

export function draftKey(scope: string, path: string): string {
  return `${scope}|${path}`;
}

/** Invalid edits belong to the document, not to a modal's mount lifetime. */
export function useFieldDraft(scope: string | undefined, path: string) {
  const key = scope ? draftKey(scope, path) : "";
  const saved = useGraphStore((s) => (key ? s.formDrafts?.[key] : undefined));
  const write = useGraphStore((s) => s.setFormDraft);
  const [local, setLocal] = useState<EditorDraft>();
  return [
    scope ? saved : local,
    (draft: EditorDraft | undefined) =>
      scope ? write(key, draft) : setLocal(draft),
  ] as const;
}

export function clearDraftBranch(scope: string | undefined, path = "") {
  if (!scope) return;
  const store = useGraphStore.getState();
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
