import { useEffect, useState } from "react";
import { Button, Group, Select, Stack, Text, Textarea, TextInput } from "@mantine/core";
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
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);
  const setDeliveryFailurePolicy = useGraphStore((s) => s.setDeliveryFailurePolicy);
  const setSelected = useGraphStore((s) => s.setSelected);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);

  if (selectedEdgeId && edge) {
    const from = nodes.find((n) => n.id === edge.source);
    const to = nodes.find((n) => n.id === edge.target);
    const port = edge.sourceHandle && edge.sourceHandle !== "out" ? `:${edge.sourceHandle}` : "";
    return (
      <Stack gap="sm" data-testid="inspector">
        <Text fw={600} size="xs" tt="uppercase" c="dimmed" style={{ letterSpacing: "0.08em" }}>
          Edge
        </Text>
        <Text fw={600} size="sm" ff="monospace">
          {from ? yamlIdOf(from) : "?"}
          {port}&gt;{to ? yamlIdOf(to) : "?"}
        </Text>
        <Text size="xs" c="dimmed">
          Wiring is typed. Drag a new connection to change it.
        </Text>
        <Button size="xs" variant="subtle" onClick={() => setSelectedEdge(null)}>
          Back to flow
        </Button>
      </Stack>
    );
  }

  if (!selectedId || !node) {
    return (
      <Stack gap="sm" data-testid="inspector">
        <Text fw={600} size="xs" tt="uppercase" c="dimmed" style={{ letterSpacing: "0.08em" }}>
          Flow
        </Text>
        <Text size="sm" c="dimmed">
          Click a node to edit it. These settings apply to the whole flow.
        </Text>
        <FlowNameField />
        <Select
          label="Delivery failure policy"
          description="Default: stop at the first failed publish. skip_failed tries every publish and returns 202 if any succeed."
          data={[
            { value: "", label: "stop_on_failure (default)" },
            { value: "stop_on_failure", label: "stop_on_failure" },
            { value: "skip_failed", label: "skip_failed" },
          ]}
          value={deliveryFailurePolicy ?? ""}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => setDeliveryFailurePolicy(v ?? "")}
        />
        {nodes.length > 0 && (
          <Stack gap={6}>
            <Text fw={600} size="xs" tt="uppercase" c="dimmed" style={{ letterSpacing: "0.08em" }}>
              Nodes
            </Text>
            {nodes.map((n) => (
              <Button
                key={n.id}
                size="xs"
                variant="default"
                fullWidth
                justify="space-between"
                aria-label={`Select ${yamlIdOf(n)}`}
                onClick={() => setSelected(n.id)}
              >
                <Group justify="space-between" wrap="nowrap" gap="xs" w="100%">
                  <Text ff="monospace" size="xs">
                    {yamlIdOf(n)}
                  </Text>
                  <Text size="xs" c="dimmed">
                    {n.data.nodeType}
                  </Text>
                </Group>
              </Button>
            ))}
          </Stack>
        )}
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
    <Stack gap="sm" data-testid="inspector">
      <Text fw={600} size="xs" tt="uppercase" c="dimmed" style={{ letterSpacing: "0.08em" }}>
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
      <Button size="xs" variant="subtle" onClick={() => setSelected(null)}>
        Back to flow
      </Button>
    </Stack>
  );
}

function FlowNameField() {
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const renameFlow = useGraphStore((s) => s.renameFlow);
  const [draft, setDraft] = useState(activeFlow);
  const [error, setError] = useState("");

  useEffect(() => {
    setDraft(activeFlow);
    setError("");
  }, [activeFlow]);

  return (
    <TextInput
      label="Flow name"
      description="YAML key. The public URL comes from the http_trigger path."
      value={draft}
      error={error || undefined}
      onChange={(e) => {
        const next = e.currentTarget.value;
        setDraft(next);
        const trimmed = next.trim();
        if (!trimmed) {
          setError("");
          return;
        }
        if (trimmed !== activeFlow && otherFlows[trimmed]) {
          setError("That flow already exists.");
          return;
        }
        setError("");
        renameFlow(trimmed);
      }}
      onBlur={() => {
        setDraft(activeFlow);
        setError("");
      }}
    />
  );
}
