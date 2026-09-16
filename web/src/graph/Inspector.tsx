import { Select, Stack, Text, Textarea, TextInput } from "@mantine/core";
import { useGraphStore } from "./store";
import { NODE_SCHEMAS, DEST_TYPES } from "./nodeSchema";
import { nodeFieldHelp, nodeHelp } from "./help";
import { SchemaForm } from "./SchemaForm";
import { Sections, type SectionSpec } from "./InspectorSections";
import { yamlIdOf } from "./model";

export function Inspector() {
  const selectedId = useGraphStore((s) => s.selectedId);
  const node = useGraphStore((s) => s.nodes.find((n) => n.id === s.selectedId));
  const updateNodeData = useGraphStore((s) => s.updateNodeData);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);
  const edge = useGraphStore((s) => s.edges.find((e) => e.id === s.selectedEdgeId));
  const nodes = useGraphStore((s) => s.nodes);
  const destinations = useGraphStore((s) => s.destinations);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const renameFlow = useGraphStore((s) => s.renameFlow);
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);
  const setDeliveryFailurePolicy = useGraphStore((s) => s.setDeliveryFailurePolicy);

  if (selectedEdgeId && edge) {
    const from = nodes.find((n) => n.id === edge.source);
    const to = nodes.find((n) => n.id === edge.target);
    const port = edge.sourceHandle && edge.sourceHandle !== "out" ? `:${edge.sourceHandle}` : "";
    return (
      <Stack gap="sm">
        <Text fw={600} size="sm" tt="uppercase" c="dimmed">
          Edge
        </Text>
        <Text fw={600} size="sm" ff="monospace">
          {from ? yamlIdOf(from) : "?"}
          {port}&gt;{to ? yamlIdOf(to) : "?"}
        </Text>
        <Text size="xs" c="dimmed">
          Wiring is typed. Drag a new connection to change it.
        </Text>
      </Stack>
    );
  }

  if (!selectedId || !node) {
    return (
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Click a node to edit it. These settings apply to the whole flow.
        </Text>
        <TextInput
          label="Flow name"
          description="YAML key. The public URL comes from the http_trigger path."
          value={activeFlow}
          onChange={(e) => renameFlow(e.currentTarget.value)}
        />
        <Select
          label="Delivery failure policy"
          description="Default: stop at the first failed publish. skip_failed tries every publish and returns 202 if any succeed."
          data={[
            { value: "", label: "stop_on_failure (default)" },
            { value: "skip_failed", label: "skip_failed" },
          ]}
          value={deliveryFailurePolicy ?? ""}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => setDeliveryFailurePolicy(v ?? "")}
        />
      </Stack>
    );
  }

  const meta = NODE_SCHEMAS[node.data.nodeType];
  const notes = node.data.notes ?? "";
  const destNames = Object.keys(destinations);
  const enums = node.data.nodeType === "publish" && destNames.length > 0 ? { destination: destNames } : undefined;
  const setOptions = (options: unknown) => updateNodeData(node.id, { options: options as Record<string, unknown> });

  const sections: SectionSpec[] = [
    {
      value: "options",
      label: "Options",
      badge: node.data.nodeType,
      render: () => (
        <SchemaForm
          schema={meta?.schema}
          value={node.data.options}
          onChange={setOptions}
          enums={enums}
          fieldHelp={(field) => nodeFieldHelp(node.data.nodeType, field)}
        />
      ),
    },
    {
      value: "notes",
      label: "Notes",
      badge: notes.trim() ? "note" : undefined,
      render: () => (
        <Textarea
          aria-label="Notes"
          placeholder="Exported as a YAML comment on the flow"
          value={notes}
          autosize
          minRows={2}
          onChange={(e) => updateNodeData(node.id, { notes: e.currentTarget.value })}
        />
      ),
    },
  ];

  return (
    <Stack gap="sm">
      <Text fw={600} size="sm" tt="uppercase" c="dimmed">
        {node.data.nodeType}
      </Text>
      {nodeHelp(node.data.nodeType) && (
        <Text size="sm" c="dimmed" lh={1.4}>
          {nodeHelp(node.data.nodeType)}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        {meta ? `${meta.input} → ${meta.output}` : ""}
        {meta?.detached ? " · detached" : ""}
      </Text>
      <TextInput
        label="Id"
        description="YAML node id used in edges, e.g. in>out."
        value={yamlIdOf(node)}
        onChange={(e) => updateNodeData(node.id, { yamlId: e.currentTarget.value })}
      />
      <Sections key={node.id} sections={sections} />
      {node.data.nodeType === "publish" && destNames.length === 0 && (
        <Text size="xs" c="dimmed">
          Add a destination in Destinations ({DEST_TYPES.length} types). Publish references it by name.
        </Text>
      )}
    </Stack>
  );
}
