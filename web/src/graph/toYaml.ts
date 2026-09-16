import { Document, isMap, isScalar } from "yaml";
import type { Notes } from "./toConfig";

function applyNotes(doc: Document, section: string, notes: Record<string, string>) {
  const map = doc.get(section);
  if (!map || !isMap(map)) return;
  for (const [name, note] of Object.entries(notes)) {
    const pair = map.items.find((p) => isScalar(p.key) && p.key.value === name);
    if (pair && isScalar(pair.key)) {
      pair.key.commentBefore = ` ${note.split("\n").join("\n ")}`;
    }
  }
}

export function toYaml(config: unknown, notes: Notes = { flows: {}, destinations: {} }): string {
  const doc = new Document(config);
  applyNotes(doc, "flows", notes.flows ?? {});
  applyNotes(doc, "destinations", notes.destinations ?? {});
  return doc.toString();
}
