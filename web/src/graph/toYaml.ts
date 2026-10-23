import { Document } from "yaml";
import { quoteLiterals, type LiteralRoots } from "./literalYaml";
export function toYaml(config: unknown, roots: LiteralRoots = []): string {
 const document = new Document(config);
 return roots.length ? quoteLiterals(document, roots) : document.toString();
}
