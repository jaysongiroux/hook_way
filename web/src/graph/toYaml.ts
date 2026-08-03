import { Document, isMap, isScalar } from "yaml";

interface Notes {
  endpoints: Record<string, string>;
  destinations: Record<string, string>;
}

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

export function toYaml(config: unknown, notes: Notes): string {
  const doc = new Document(config);
  applyNotes(doc, "endpoints", notes.endpoints);
  applyNotes(doc, "destinations", notes.destinations);
  return doc.toString();
}
