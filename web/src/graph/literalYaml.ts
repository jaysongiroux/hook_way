import {
  isMap,
  isScalar,
  isSeq,
  parseDocument,
  Scalar,
  type Node,
  type Document,
} from "yaml";
import type { GraphDocument } from "./model";
type Path = (string | number)[];
export type LiteralRoots = Path[];
function walk(
  node: Node | null | undefined,
  path: Path,
  fn: (node: Scalar, path: Path) => void,
) {
  if (isScalar(node)) fn(node, path);
  if (isMap(node))
    for (const pair of node.items) {
      const next = [...path, String(pair.key)];
      if (isScalar(pair.key)) fn(pair.key, next);
      walk(pair.value as Node, next, fn);
    }
  if (isSeq(node))
    node.items.forEach((value, index) =>
      walk(value as Node, [...path, index], fn),
    );
}
export function literalRoots(doc: GraphDocument, flow?: string): LiteralRoots {
  const roots: LiteralRoots = flow
    ? []
    : (doc.literalConfigFields ?? []).map((key) => [key]);
  const flows = { ...doc.otherFlows, [doc.activeFlow]: { nodes: doc.nodes } };
  for (const [name, snapshot] of Object.entries(flows)) {
    if (flow && name !== flow) continue;
    snapshot.nodes.forEach((node, index) => {
      if (node.data.literalDollars)
        roots.push(flow ? ["nodes", index] : ["flows", name, "nodes", index]);
    });
  }
  return roots;
}
export function quoteLiterals(document: Document, roots: LiteralRoots) {
  const selected = (path: Path) =>
    roots.some((root) => root.every((part, i) => path[i] === part));
  walk(document.contents, [], (node, path) => {
    if (
      selected(path) &&
      typeof node.value === "string" &&
      node.value.includes("$")
    )
      node.type = Scalar.QUOTE_DOUBLE;
  });
  const text = document.toString();
  const replacements: [number, number, string][] = [];
  walk(parseDocument(text).contents, [], (node, path) => {
    if (
      selected(path) &&
      typeof node.value === "string" &&
      node.value.includes("$") &&
      node.range
    ) {
      const [start, end] = node.range;
      replacements.push([
        start,
        end,
        text.slice(start, end).replaceAll("$", "\\u0024"),
      ]);
    }
  });
  let result = text;
  for (const [start, end, value] of replacements.sort((a, b) => b[0] - a[0]))
    result = result.slice(0, start) + value + result.slice(end);
  return result;
}

/** Recover generated literal intent from YAML without exporting editor metadata. */
export function restoreLiteralProvenance(
  doc: GraphDocument,
  text: string,
  flowName?: string,
) {
  const document = parseDocument(text);
  walk(document.contents, [], (node, relativePath) => {
    const path = flowName ? ["flows", flowName, ...relativePath] : relativePath;
    if (
      typeof node.value !== "string" ||
      !node.value.includes("$") ||
      !node.range ||
      !text.slice(node.range[0], node.range[1]).includes("\\u0024")
    )
      return;
    if (
      path[0] === "flows" &&
      path[2] === "nodes" &&
      typeof path[3] === "number"
    ) {
      const flow = String(path[1]);
      const nodes =
        flow === doc.activeFlow ? doc.nodes : doc.otherFlows[flow]?.nodes;
      const target = nodes?.[path[3]];
      if (target) target.data.literalDollars = true;
    } else if (typeof path[0] === "string" && path[0] !== "flows") {
      doc.literalConfigFields = [
        ...new Set([...(doc.literalConfigFields ?? []), path[0]]),
      ];
    }
  });
  return doc;
}
