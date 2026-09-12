import { Anchor, Divider, Group, Stack, Switch, Text, Textarea, TextInput } from "@mantine/core";
import { useGraphStore } from "./store";
import type { DestinationData, EndpointData, FilterData } from "./model";
import { destinationRoutes, endpointRoutes, owningEndpointId } from "./routes";
import { Sections, type SectionSpec } from "./InspectorSections";
import { RouteRowList } from "./RouteRows";
import { EndpointAuthFields, EndpointRequestFields } from "./EndpointForm";
import { VariablesSection } from "./VariablesSection";
import { DestinationSettingsFields } from "./DestinationForm";
import { FilterConditionFields } from "./FilterForm";

export function Inspector() {
  const selectedId = useGraphStore((s) => s.selectedId);
  const node = useGraphStore((s) => s.nodes.find((n) => n.id === s.selectedId));
  const updateNodeData = useGraphStore((s) => s.updateNodeData);
  const selectedEdgeId = useGraphStore((s) => s.selectedEdgeId);
  const edge = useGraphStore((s) => s.edges.find((e) => e.id === s.selectedEdgeId));
  const edges = useGraphStore((s) => s.edges);
  const updateEdgeData = useGraphStore((s) => s.updateEdgeData);
  const setSelected = useGraphStore((s) => s.setSelected);
  const nodes = useGraphStore((s) => s.nodes);

  if (selectedEdgeId && edge) {
    const data = (edge.data ?? { target: "", create: true }) as { target: string; create: boolean };
    const epId = owningEndpointId(edge.id, nodes, edges);
    const epName = nodes.find((n) => n.id === epId)?.data.name;
    const destName = nodes.find((n) => n.id === edge.target)?.data.name;
    return (
      <Stack gap="sm">
        <Text fw={600} size="sm" tt="uppercase" c="dimmed">
          Route
        </Text>
        <Text fw={600} size="sm">
          {String(epName ?? "?")} → {String(destName ?? "?")}
        </Text>
        <TextInput
          label="Target"
          placeholder="queue / topic name"
          description="A name, or a template with {variable} placeholders declared on the endpoint"
          value={data.target}
          onChange={(e) => updateEdgeData(edge.id, { target: e.currentTarget.value })}
        />
        <Switch
          label="Create target if missing"
          checked={data.create}
          onChange={(e) => updateEdgeData(edge.id, { create: e.currentTarget.checked })}
        />
        <Divider />
        <Group gap="md">
          {epId && (
            <Anchor size="xs" onClick={() => setSelected(epId)}>
              Edit endpoint
            </Anchor>
          )}
          <Anchor size="xs" onClick={() => setSelected(edge.target)}>
            Open destination
          </Anchor>
        </Group>
      </Stack>
    );
  }

  if (!selectedId || !node) {
    return (
      <Text size="sm" c="dimmed">
        Select a node to edit its properties.
      </Text>
    );
  }

  const set = (patch: Record<string, unknown>) => updateNodeData(node.id, patch);
  const notes = (node.data as { notes?: string }).notes ?? "";

  const notesSection: SectionSpec = {
    value: "notes",
    label: "Notes",
    badge: notes.trim() ? "note" : undefined,
    render: () => (
      // The section header already reads "Notes", so the field carries an
      // accessible name without repeating the label on screen.
      <Textarea
        aria-label="Notes"
        placeholder="Exported as a YAML comment"
        value={notes}
        autosize
        minRows={2}
        onChange={(e) => set({ notes: e.currentTarget.value })}
      />
    ),
  };

  const sections: SectionSpec[] = [];
  if (node.data.kind === "endpoint") {
    const data = node.data as EndpointData;
    const routes = endpointRoutes(node.id, nodes, edges);
    sections.push(
      { value: "request", label: "Request", render: () => <EndpointRequestFields data={data} set={set} /> },
      {
        value: "auth",
        label: "Authentication",
        badge: data.auth.type,
        render: () => <EndpointAuthFields data={data} set={set} />,
      },
      {
        value: "variables",
        label: "Variables",
        badge: (data.variables ?? []).length || undefined,
        render: () => <VariablesSection data={data} set={set} />,
      },
      {
        value: "routes",
        label: "Routes",
        badge: routes.length || undefined,
        render: () => (
          <RouteRowList
            rows={routes.map((r) => ({
              edgeId: r.edgeId,
              caption: `→ ${r.dest}`,
              target: r.target,
              aria: `route to ${r.dest}: ${r.target || "(no target)"}`,
            }))}
            emptyText="Draw a link to a destination to add a route."
          />
        ),
      },
      notesSection,
    );
  } else if (node.data.kind === "destination") {
    const data = node.data as DestinationData;
    const incoming = destinationRoutes(node.id, nodes, edges);
    sections.push(
      {
        value: "settings",
        label: "Settings",
        badge: data.destType,
        render: () => <DestinationSettingsFields data={data} set={set} />,
      },
      {
        value: "incoming",
        label: "Incoming routes",
        badge: incoming.length || undefined,
        render: () => (
          <RouteRowList
            rows={incoming.map((r) => ({
              edgeId: r.edgeId,
              caption: `from ${r.endpoint}`,
              target: r.target,
              aria: `route from ${r.endpoint}: ${r.target || "(no target)"}`,
            }))}
            emptyText="No endpoints route here yet."
          />
        ),
      },
      notesSection,
    );
  } else {
    const data = node.data as FilterData;
    sections.push(
      { value: "condition", label: "Condition", render: () => <FilterConditionFields data={data} set={set} /> },
      notesSection,
    );
  }

  return (
    <Stack gap="sm">
      <Text fw={600} size="sm" tt="uppercase" c="dimmed">
        {String(node.data.kind)}
      </Text>
      <TextInput label="Name" value={String(node.data.name)} onChange={(e) => set({ name: e.currentTarget.value })} />
      {/* Keyed by node so the open section resets when a different node is selected. */}
      <Sections key={node.id} sections={sections} />
    </Stack>
  );
}
