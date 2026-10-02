import { useState, type ReactNode } from "react";
import {
  Alert,
  Button,
  Group,
  NativeSelect,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { asRecord, useRowKeys } from "./ConditionEditor";
import {
  defaultNodeOptions,
  labelledPorts,
  NODE_SCHEMAS,
  nodeHasOnError,
  nodeLabel,
} from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";

const CHILD_TYPES = [
  "transform",
  "code",
  "filter",
  "if_else",
  "switch",
  "publish",
];
const validID = (value: string) =>
  value.length > 0 &&
  new TextEncoder().encode(value).length <= 1024 &&
  !/[\s:>\p{Cc}]/u.test(value);
const permitted = (type: string) =>
  CHILD_TYPES.includes(type) && !!NODE_SCHEMAS[type];

interface ChildEdge {
  from: string;
  port: string;
  to: string;
}
function readEdge(value: unknown): ChildEdge | undefined {
  if (typeof value !== "string") return undefined;
  const halves = value.trim().split(">");
  if (halves.length !== 2) return undefined;
  const left = halves[0].split(":");
  if (left.length > 2) return undefined;
  return {
    from: left[0].trim(),
    port: (left[1] ?? "").trim(),
    to: halves[1].trim(),
  };
}
function writeEdge(edge: ChildEdge) {
  return `${edge.from}${edge.port ? `:${edge.port}` : ""}>${edge.to}`;
}
function withImportedChoice(
  choices: { value: string; label: string }[],
  value: unknown,
) {
  const text = String(value ?? "");
  return choices.some((option) => option.value === text)
    ? choices
    : [
        { value: text, label: text ? `Imported: ${text}` : "Choose a node" },
        ...choices,
      ];
}

export function batchChildIssues(value: unknown, parentId?: string): string[] {
  const child = asRecord(value);
  if (!child)
    return [
      "Child flow must be an object. Replace the imported value to start a child flow.",
    ];
  const issues: string[] = [];
  const nodes: unknown[] = Array.isArray(child.nodes) ? child.nodes : [];
  const edges: unknown[] = Array.isArray(child.edges) ? child.edges : [];
  if (!Array.isArray(child.nodes)) issues.push("Child nodes must be a list.");
  if (nodes.length === 0 || nodes.length > 64)
    issues.push("A child flow needs between 1 and 64 nodes.");
  const records = nodes.map(asRecord);
  if (records.some((node) => !node))
    issues.push(
      "Each child node must be an object; replace or remove invalid rows.",
    );
  const ids = records.map((node) => String(node?.id ?? ""));
  if (ids.some((id) => !validID(id)))
    issues.push(
      "Every child node needs an ID without whitespace, control characters, : or > (at most 1024 bytes).",
    );
  if (new Set(ids).size !== ids.length)
    issues.push("Child node IDs must be unique.");
  if (
    parentId &&
    ids.some(
      (id) =>
        !id.startsWith(`${parentId}.`) || id.length <= parentId.length + 1,
    )
  )
    issues.push(
      `Every child node ID must start with ${parentId}. followed by a name.`,
    );
  if (records.some((node) => node && !permitted(String(node.type ?? ""))))
    issues.push(
      "Children only support transform, code, filter, if/else, switch, and publish. Unsupported imported nodes are retained.",
    );
  if (records.filter((node) => node?.type === "publish").length !== 1)
    issues.push("A child flow must contain exactly one publish node.");
  const entry = String(child.entry ?? "");
  if (!ids.includes(entry) || !validID(entry))
    issues.push("Choose an entry node from this child flow.");
  if (child.edges !== undefined && !Array.isArray(child.edges))
    issues.push(
      "Child edges must be a list; the imported value is retained until replaced.",
    );
  const seenEdges = new Set<string>();
  const adjacent = new Map<string, string[]>();
  edges.forEach((raw, index) => {
    const edge = readEdge(raw);
    if (
      !edge ||
      !validID(edge.from) ||
      !validID(edge.to) ||
      (edge.port !== "" && !validID(edge.port))
    ) {
      issues.push(
        `Edge ${index + 1} needs a valid source, output port, and target.`,
      );
      return;
    }
    const from = records.find((node) => node?.id === edge.from);
    const to = records.find((node) => node?.id === edge.to);
    if (!from || !to) {
      issues.push(
        `Edge ${index + 1} references a node outside this child flow.`,
      );
      return;
    }
    const signature = writeEdge(edge);
    if (seenEdges.has(signature))
      issues.push(`Edge ${index + 1} duplicates another connection.`);
    seenEdges.add(signature);
    const fromType = String(from.type);
    const ports = labelledPorts(fromType, from);
    if (
      edge.port === "on_error"
        ? !nodeHasOnError(fromType)
        : ports.length > 0
          ? !ports.includes(edge.port)
          : edge.port !== ""
    )
      issues.push(
        `Edge ${index + 1} selects an output port this node does not expose.`,
      );
    if (
      NODE_SCHEMAS[fromType] &&
      NODE_SCHEMAS[String(to.type)] &&
      NODE_SCHEMAS[fromType].output !== NODE_SCHEMAS[String(to.type)].input
    )
      issues.push(`Edge ${index + 1} connects incompatible node types.`);
    adjacent.set(edge.from, [...(adjacent.get(edge.from) ?? []), edge.to]);
  });
  if (ids.includes(entry)) {
    const reached = new Set<string>();
    const visiting = new Set<string>();
    let cycle = false;
    const visit = (id: string) => {
      if (visiting.has(id)) {
        cycle = true;
        return;
      }
      if (reached.has(id)) return;
      reached.add(id);
      visiting.add(id);
      for (const next of adjacent.get(id) ?? []) visit(next);
      visiting.delete(id);
    };
    visit(entry);
    if (cycle) issues.push("Child flow connections must not contain a cycle.");
    if (ids.some((id) => !reached.has(id)))
      issues.push("Every child node must be reachable from the entry node.");
    if (
      records.some(
        (node) =>
          node &&
          permitted(String(node.type)) &&
          node.type !== "publish" &&
          !adjacent.has(String(node.id)),
      )
    )
      issues.push(
        "Connect every processing node to a next node; publish is the terminal node.",
      );
  }
  return issues;
}

export interface BatchChildEditorProps {
  value: unknown;
  onChange: (value: unknown) => void;
  scope?: string;
  parentId?: string;
  renderNodeOptions?: (
    nodeType: string,
    value: Record<string, unknown>,
    onChange: (value: Record<string, unknown>) => void,
    scope: string,
  ) => ReactNode;
}

export function BatchChildEditor({
  value,
  onChange,
  scope = "batch-child",
  parentId,
  renderNodeOptions,
}: BatchChildEditorProps) {
  const child = asRecord(value);
  const nodes: unknown[] = Array.isArray(child?.nodes) ? child.nodes : [];
  const edges: unknown[] = Array.isArray(child?.edges) ? child.edges : [];
  const nodeRows = useRowKeys(nodes, `${scope}/nodes`);
  const edgeRows = useRowKeys(edges, `${scope}/edges`);
  const [newType, setNewType] = useState("transform");
  if (!child)
    return (
      <Stack gap="xs">
        <Alert color="red">
          Child flow must be an object. The original imported value is retained
          until you replace it.
        </Alert>
        <Button onClick={() => onChange({ entry: "", nodes: [], edges: [] })}>
          Create child flow
        </Button>
      </Stack>
    );
  const publishCount = nodes.filter(
    (node) => asRecord(node)?.type === "publish",
  ).length;
  const choices = nodes
    .flatMap((raw) => {
      const node = asRecord(raw);
      return node && typeof node.id === "string" && node.id
        ? [{ value: node.id, label: node.id }]
        : [];
    })
    .filter(
      (item, index, all) =>
        all.findIndex((other) => other.value === item.value) === index,
    );
  const issues = batchChildIssues(child, parentId);
  const updateNodes = (
    next: unknown[],
    keys = nodeRows.keys,
    extra: Record<string, unknown> = {},
  ) => {
    nodeRows.accept(next, keys);
    onChange({ ...child, nodes: next, ...extra });
  };
  const updateNode = (index: number, next: unknown) =>
    updateNodes(nodes.map((node, i) => (i === index ? next : node)));
  const removeNode = (index: number) => {
    useGraphStore
      .getState()
      .clearFormDrafts(`${scope}.nodes.${nodeRows.keys[index]}`);
    updateNodes(
      nodes.filter((_, i) => i !== index),
      nodeRows.keys.filter((_, i) => i !== index),
    );
  };
  const updateEdges = (next: unknown[], keys = edgeRows.keys) => {
    edgeRows.accept(next, keys);
    onChange({ ...child, edges: next });
  };
  return (
    <Stack gap="sm" data-form-scope={scope}>
      <Text size="sm">
        Run this child flow for each admitted event. Use exactly one publish
        node; triggers, responses, HTTP requests, and nested batches are not
        allowed.
      </Text>
      <Text size="xs">
        {parentId
          ? `Child IDs must start with ${parentId}. — for example ${parentId}.send.`
          : "Child IDs must start with the parent batch node ID plus a dot — for example batch.send."}
      </Text>
      {issues.length > 0 && (
        <Alert color="red" title="Child flow needs attention">
          <Stack gap={4}>
            {issues.map((issue) => (
              <Text key={issue} size="sm">
                {issue}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}
      <NativeSelect
        label="Child entry node"
        aria-label="Child entry node"
        required
        value={String(child.entry ?? "")}
        data={withImportedChoice(choices, child.entry)}
        onChange={(event) =>
          onChange({ ...child, entry: event.currentTarget.value })
        }
      />
      {!Array.isArray(child.nodes) && child.nodes !== undefined && (
        <Button variant="light" onClick={() => updateNodes([], [])}>
          Replace imported node list
        </Button>
      )}
      {nodes.map((raw, index) => {
        const node = asRecord(raw);
        if (!node)
          return (
            <Stack key={nodeRows.keys[index]}>
              <Alert color="red">
                Child node {index + 1} is not an object and has been retained.
              </Alert>
              <Button
                onClick={() =>
                  updateNode(index, {
                    id: "",
                    type: "transform",
                    ...defaultNodeOptions("transform", ""),
                  })
                }
              >
                Replace child node {index + 1}
              </Button>
              <Button color="red" onClick={() => removeNode(index)}>
                Remove child node {index + 1}
              </Button>
            </Stack>
          );
        const type = String(node.type ?? "");
        const options = { ...node };
        delete options.id;
        delete options.type;
        delete options.description;
        return (
          <Stack
            key={nodeRows.keys[index]}
            gap="xs"
            role="group"
            aria-label={`Child node ${index + 1}`}
          >
            <TextInput
              label={`Child node ${index + 1} ID`}
              aria-label={`Child node ${index + 1} ID`}
              required
              value={String(node.id ?? "")}
              onChange={(event) => {
                const id = event.currentTarget.value;
                const old = String(node.id ?? "");
                const unique =
                  nodes.filter((other) => asRecord(other)?.id === old)
                    .length === 1;
                const mappedEdges = unique
                  ? edges.map((item) => {
                      const edge = readEdge(item);
                      if (!edge) return item;
                      return writeEdge({
                        ...edge,
                        from: edge.from === old ? id : edge.from,
                        to: edge.to === old ? id : edge.to,
                      });
                    })
                  : edges;
                edgeRows.accept(mappedEdges, edgeRows.keys);
                updateNodes(
                  nodes.map((item, i) =>
                    i === index ? { ...node, id } : item,
                  ),
                  nodeRows.keys,
                  {
                    edges: mappedEdges,
                    ...(unique && child.entry === old ? { entry: id } : {}),
                  },
                );
              }}
            />
            <NativeSelect
              label={`Child node ${index + 1} type`}
              aria-label={`Child node ${index + 1} type`}
              required
              value={type}
              data={withImportedChoice(
                CHILD_TYPES.filter((item) => NODE_SCHEMAS[item]).map(
                  (item) => ({ value: item, label: nodeLabel(item) }),
                ),
                type,
              )}
              description="Changing the node type replaces its options with that type's starting values. ID and description are kept."
              error={
                permitted(type)
                  ? undefined
                  : "Choose a permitted child node type. The unsupported imported node is retained until changed or removed."
              }
              onChange={(event) => {
                const selected = event.currentTarget.value;
                useGraphStore
                  .getState()
                  .clearFormDrafts(`${scope}.nodes.${nodeRows.keys[index]}`);
                updateNode(index, {
                  id: node.id,
                  type: selected,
                  ...(node.description !== undefined
                    ? { description: node.description }
                    : {}),
                  ...defaultNodeOptions(selected, String(node.id ?? "")),
                });
              }}
            />
            {permitted(type) &&
              (renderNodeOptions ? (
                renderNodeOptions(
                  type,
                  options,
                  (next) => {
                    updateNode(index, {
                      ...next,
                      id: node.id,
                      type,
                      ...(node.description !== undefined
                        ? { description: node.description }
                        : {}),
                    });
                  },
                  `${scope}.nodes.${nodeRows.keys[index]}`,
                )
              ) : (
                <SchemaForm
                  schema={NODE_SCHEMAS[type].schema}
                  value={options}
                  scope={`${scope}.nodes.${nodeRows.keys[index]}`}
                  onChange={(next) =>
                    updateNode(index, {
                      ...asRecord(next),
                      id: node.id,
                      type,
                      ...(node.description !== undefined
                        ? { description: node.description }
                        : {}),
                    })
                  }
                />
              ))}
            {!permitted(type) && (
              <Text size="xs" style={{ overflowWrap: "anywhere" }}>
                {JSON.stringify(node)}
              </Text>
            )}
            <Button
              variant="subtle"
              color="red"
              size="xs"
              onClick={() => removeNode(index)}
            >
              Remove child node {index + 1}
            </Button>
          </Stack>
        );
      })}
      <NativeSelect
        label="New child node type"
        aria-label="New child node type"
        value={newType}
        data={CHILD_TYPES.filter((type) => NODE_SCHEMAS[type]).map((type) => ({
          value: type,
          label: nodeLabel(type),
        }))}
        onChange={(event) => setNewType(event.currentTarget.value)}
      />
      <Group>
        <Button
          variant="light"
          size="xs"
          disabled={
            (child.nodes !== undefined && !Array.isArray(child.nodes)) ||
            nodes.length >= 64 ||
            (newType === "publish" && publishCount >= 1)
          }
          onClick={() => {
            const inferred = String(asRecord(nodes[0])?.id ?? "")
              .split(".")
              .slice(0, -1)
              .join(".");
            const prefix = parentId || inferred || "batch";
            let suffix = 1;
            let id = `${prefix}.${newType}`;
            while (nodes.some((node) => asRecord(node)?.id === id))
              id = `${prefix}.${newType}${suffix++}`;
            const next = [
              ...nodes,
              { id, type: newType, ...defaultNodeOptions(newType, id) },
            ];
            updateNodes(
              next,
              [...nodeRows.keys, nodeRows.create()],
              child.entry ? {} : { entry: id },
            );
          }}
        >
          Add child node
        </Button>
      </Group>
      {newType === "publish" && publishCount >= 1 && (
        <Text size="xs">
          A publish node already exists. Edit it or remove it before adding
          another.
        </Text>
      )}
      <Text fw={600} size="sm">
        Child connections
      </Text>
      <Text size="xs">
        Choose a source, its output port, and a target. The exported edge is a
        string such as batch.select:true&gt;batch.send.
      </Text>
      {edges.map((raw, index) => {
        const edge = readEdge(raw);
        if (!edge)
          return (
            <Stack key={edgeRows.keys[index]}>
              <Alert color="red">
                Edge {index + 1} has an unsupported format and is retained:{" "}
                {JSON.stringify(raw)}
              </Alert>
              <Button
                onClick={() =>
                  updateEdges(
                    edges.map((item, i) => (i === index ? ">" : item)),
                  )
                }
              >
                Replace edge {index + 1}
              </Button>
              <Button
                color="red"
                onClick={() =>
                  updateEdges(
                    edges.filter((_, i) => i !== index),
                    edgeRows.keys.filter((_, i) => i !== index),
                  )
                }
              >
                Remove edge {index + 1}
              </Button>
            </Stack>
          );
        const source = nodes
          .map(asRecord)
          .find((node) => node?.id === edge.from);
        const sourceType = String(source?.type ?? "");
        const ports = labelledPorts(sourceType, source ?? {});
        const portChoices = [
          ...(ports.length
            ? ports.map((port) => ({ value: port, label: port }))
            : [{ value: "", label: "Default output" }]),
          ...(nodeHasOnError(sourceType)
            ? [{ value: "on_error", label: "On error" }]
            : []),
        ];
        const change = (next: ChildEdge) =>
          updateEdges(
            edges.map((item, i) => (i === index ? writeEdge(next) : item)),
          );
        return (
          <Stack
            key={edgeRows.keys[index]}
            gap="xs"
            role="group"
            aria-label={`Child edge ${index + 1}`}
          >
            <NativeSelect
              label={`Edge ${index + 1} source`}
              aria-label={`Edge ${index + 1} source`}
              value={edge.from}
              data={withImportedChoice(choices, edge.from)}
              onChange={(event) => {
                const from = event.currentTarget.value;
                const selected = nodes
                  .map(asRecord)
                  .find((node) => node?.id === from);
                const nextPorts = labelledPorts(
                  String(selected?.type ?? ""),
                  selected ?? {},
                );
                change({ ...edge, from, port: nextPorts[0] ?? "" });
              }}
            />
            <NativeSelect
              label={`Edge ${index + 1} output port`}
              aria-label={`Edge ${index + 1} output port`}
              value={edge.port}
              data={withImportedChoice(portChoices, edge.port)}
              onChange={(event) =>
                change({ ...edge, port: event.currentTarget.value })
              }
            />
            <NativeSelect
              label={`Edge ${index + 1} target`}
              aria-label={`Edge ${index + 1} target`}
              value={edge.to}
              data={withImportedChoice(choices, edge.to)}
              onChange={(event) =>
                change({ ...edge, to: event.currentTarget.value })
              }
            />
            <Button
              variant="subtle"
              color="red"
              size="xs"
              onClick={() =>
                updateEdges(
                  edges.filter((_, i) => i !== index),
                  edgeRows.keys.filter((_, i) => i !== index),
                )
              }
            >
              Remove edge {index + 1}
            </Button>
          </Stack>
        );
      })}
      <Button
        variant="light"
        size="xs"
        onClick={() => {
          const next = [...edges, ">"];
          updateEdges(next, [...edgeRows.keys, edgeRows.create()]);
        }}
      >
        {child.edges !== undefined && !Array.isArray(child.edges)
          ? "Replace with an edge list"
          : "Add child edge"}
      </Button>
    </Stack>
  );
}
