import {
  isMap,
  isScalar,
  isSeq,
  parseDocument,
  Scalar,
  type Node,
  type Document,
} from "yaml";
import type { ConfigPath, GraphDocument } from "./model";
type Path = ConfigPath;
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
  const roots: LiteralRoots = flow ? [] : [...(doc.literalConfigPaths ?? [])];
  if (!flow) {
    const events = doc.configExtra.events;
    if (events && typeof events === "object" && !Array.isArray(events))
      for (const name of Object.keys(events)) roots.push(["events", name, "payload_schema"]);
  }
  const flows = { ...doc.otherFlows, [doc.activeFlow]: { nodes: doc.nodes } };
  for (const [name, snapshot] of Object.entries(flows)) {
    if (flow && name !== flow) continue;
    snapshot.nodes.forEach((node, index) => {
      for (const path of node.data.literalDollarPaths ?? [])
        roots.push([
          ...(flow ? ["nodes", index] : ["flows", name, "nodes", index]),
          ...path,
        ]);
    });
  }
  return roots;
}
export function quoteLiterals(document: Document, roots: LiteralRoots) {
  const selected = (path: Path) =>
    roots.some((root) => {
      const eventSchema = root[0] === "events" && root.length === 3 && root[2] === "payload_schema";
      const schema = eventSchema ||
        root.at(-3) === "tools" &&
        typeof root.at(-2) === "number" &&
        ["input_schema", "output_schema"].includes(String(root.at(-1)));
      return (
        (schema || path.length === root.length) &&
        root.every((part, i) => path[i] === part)
      );
    });
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
      if (target) {
        const relative = path.slice(4);
        // Imported JSON Schemas stay literal, including edited enum/default strings.
        const root =
          relative[0] === "tools" &&
          ["input_schema", "output_schema"].includes(String(relative[2]))
            ? relative.slice(0, 3)
            : relative;
        target.data.literalDollarPaths = addRoot(
          target.data.literalDollarPaths,
          root,
        );
        if (relative[0] === "tools" && typeof relative[1] === "number") {
          for (const field of ["input_schema", "output_schema"]) {
            target.data.literalDollarPaths = addRoot(
              target.data.literalDollarPaths,
              ["tools", relative[1], field],
            );
          }
        }
      }
    } else if (typeof path[0] === "string" && path[0] !== "flows") {
      doc.literalConfigPaths = addRoot(doc.literalConfigPaths, path);
    }
  });
  return doc;
}

function addRoot(paths: Path[] = [], root: Path): Path[] {
  return paths.some((path) => JSON.stringify(path) === JSON.stringify(root))
    ? paths
    : [...paths, root];
}
const object = (value: unknown): Record<string, unknown> =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};

/** Move provenance with named array entries and renamed map keys, and drop deleted fields. */
export function reconcileLiteralPaths(
  paths: Path[] = [],
  before: unknown,
  after: unknown,
): Path[] {
  return paths.flatMap((path) => {
    let previous = before,
      next = after;
    const result: Path = [];
    for (const part of path) {
      let destination = part;
      if (
        Array.isArray(previous) &&
        Array.isArray(next) &&
        typeof part === "number"
      ) {
        const item = object(previous[part]);
        const identity =
          typeof item.name === "string"
            ? "name"
            : typeof item.id === "string"
              ? "id"
              : undefined;
        if (identity) {
          let index = next.findIndex(
            (value) => object(value)[identity] === item[identity],
          );
          if (index < 0) {
            const oldIDs = new Set(
              previous.map((value) => object(value)[identity]),
            );
            const newIDs = new Set(
              next.map((value) => object(value)[identity]),
            );
            const removed = previous.filter(
              (value) => !newIDs.has(object(value)[identity]),
            );
            const added = next
              .map((value, index) => ({ value, index }))
              .filter(({ value }) => !oldIDs.has(object(value)[identity]));
            if (removed.length === 1 && added.length === 1)
              index = added[0].index;
          }
          if (index < 0) return [];
          destination = index;
        }
        if (!(destination in next)) return [];
        previous = previous[part];
        next = next[Number(destination)];
      } else {
        const old = object(previous),
          updated = object(next);
        if (!Object.hasOwn(updated, part)) {
          const matches = Object.keys(updated).filter(
            (key) =>
              !Object.hasOwn(old, key) &&
              JSON.stringify(updated[key]) === JSON.stringify(old[part]),
          );
          if (matches.length !== 1) return [];
          destination = matches[0];
        }
        previous = old[part];
        next = updated[destination];
      }
      result.push(destination);
    }
    return [result];
  });
}
