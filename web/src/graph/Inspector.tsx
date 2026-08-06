import {
  Stack,
  Text,
  TextInput,
  Select,
  Textarea,
  SegmentedControl,
  Switch,
  NumberInput,
  Divider,
  Fieldset,
  UnstyledButton,
  Anchor,
  Group,
} from "@mantine/core";
import { useGraphStore } from "./store";
import type { AuthData, EndpointData, DestinationData, FilterData } from "./model";
import { destinationRoutes, endpointRoutes, owningEndpointId } from "./routes";
import type { FieldSpec } from "./destTypes";
import {
  DEST_TYPES,
  DEST_TYPE_SPECS,
  defaultSettings,
  AUTH_TYPES,
  FILTER_SOURCES,
  OPERATORS,
  SCHEMES_BY_AUTH,
  defaultScheme,
} from "./destTypes";

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

  return (
    <Stack gap="sm">
      <Text fw={600} size="sm" tt="uppercase" c="dimmed">
        {String(node.data.kind)}
      </Text>
      <TextInput
        label="Name"
        value={String(node.data.name)}
        onChange={(e) => set({ name: e.currentTarget.value })}
      />

      {node.data.kind === "endpoint" && (
        <>
          <EndpointForm data={node.data as EndpointData} set={set} />
          <RoutesSection endpointId={node.id} />
        </>
      )}
      {node.data.kind === "filter" && <FilterForm data={node.data as FilterData} set={set} />}
      {node.data.kind === "destination" && (
        <>
          <DestinationForm data={node.data as DestinationData} set={set} />
          <IncomingRoutesSection destId={node.id} />
        </>
      )}

      <Divider />
      <Textarea
        label="Notes"
        placeholder="Exported as a YAML comment"
        value={notes}
        autosize
        minRows={2}
        onChange={(e) => set({ notes: e.currentTarget.value })}
      />
    </Stack>
  );
}

function EndpointForm({ data, set }: { data: EndpointData; set: (p: Record<string, unknown>) => void }) {
  const auth = data.auth;
  const setAuth = (patch: Partial<AuthData>) => set({ auth: { ...auth, ...patch } });
  return (
    <Stack gap="xs">
      <TextInput label="Path" value={data.path} onChange={(e) => set({ path: e.currentTarget.value })} />
      <Select
        label="Auth type"
        data={AUTH_TYPES}
        value={auth.type}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => {
          const t = v ?? "none";
          setAuth({ type: t, scheme: defaultScheme(t) });
        }}
      />
      {(auth.type === "hmac_sha256" || auth.type === "jwt") && (
        <Select
          label="Scheme"
          data={SCHEMES_BY_AUTH[auth.type] ?? []}
          value={auth.scheme}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => setAuth({ scheme: v ?? defaultScheme(auth.type) })}
        />
      )}
      {auth.type !== "none" && (
        <>
          <TextInput label="Signature header" value={auth.signatureHeader} onChange={(e) => setAuth({ signatureHeader: e.currentTarget.value })} />
          <SegmentedControl
            fullWidth
            size="xs"
            data={[{ label: "Literal", value: "literal" }, { label: "Env var", value: "env" }]}
            value={auth.secretMode}
            onChange={(v) => setAuth({ secretMode: v as AuthData["secretMode"] })}
          />
          <TextInput
            label={auth.secretMode === "env" ? "Secret env var name" : "Secret (literal)"}
            placeholder={auth.secretMode === "env" ? "GITHUB_WEBHOOK_SECRET" : "raw secret value"}
            value={auth.secret}
            onChange={(e) => setAuth({ secret: e.currentTarget.value })}
          />
          {auth.secretMode === "literal" && (
            <Text size="xs" c="orange">
              A literal secret is written into exported YAML and the local cache.
            </Text>
          )}
        </>
      )}
    </Stack>
  );
}

function FilterForm({ data, set }: { data: FilterData; set: (p: Record<string, unknown>) => void }) {
  return (
    <Stack gap="xs">
      <Select
        label="Source"
        data={FILTER_SOURCES}
        value={data.source}
        onChange={(v) => set({ source: v ?? "body" })}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
      />
      <TextInput label="Path" placeholder="e.g. action or headers.X-Event" value={data.path} onChange={(e) => set({ path: e.currentTarget.value })} />
      <Select
        label="Operator"
        data={OPERATORS}
        value={data.operator}
        onChange={(v) => set({ operator: v ?? "eq" })}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
      />
      <TextInput label="Value" value={String(data.value ?? "")} onChange={(e) => set({ value: e.currentTarget.value })} />
    </Stack>
  );
}

// renderField renders one destination setting control by kind, recursing on
// "group" fields into a labeled Fieldset. onChange receives the field's new
// value; a group's onChange receives the merged nested object.
function renderField(f: FieldSpec, val: unknown, onChange: (v: unknown) => void) {
  if (f.kind === "group") {
    const groupVal = (val ?? {}) as Record<string, unknown>;
    return (
      <Fieldset key={f.key} legend={f.label} radius="sm" p="xs">
        <Stack gap="xs">
          {(f.fields ?? []).map((child) =>
            renderField(child, groupVal[child.key], (cv) => onChange({ ...groupVal, [child.key]: cv })),
          )}
        </Stack>
      </Fieldset>
    );
  }
  if (f.kind === "select") {
    return (
      <Select
        key={f.key}
        label={f.label}
        data={f.options ?? []}
        value={String(val ?? "")}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => onChange(v ?? "")}
      />
    );
  }
  if (f.kind === "bool") {
    return (
      <Switch key={f.key} label={f.label} checked={Boolean(val)} onChange={(e) => onChange(e.currentTarget.checked)} />
    );
  }
  if (f.kind === "number") {
    return (
      <NumberInput
        key={f.key}
        label={f.label}
        value={val === "" || val == null ? "" : Number(val)}
        onChange={(v) => onChange(v)}
        min={0}
      />
    );
  }
  return (
    <TextInput
      key={f.key}
      label={f.label}
      placeholder={f.placeholder}
      value={String(val ?? "")}
      onChange={(e) => onChange(e.currentTarget.value)}
    />
  );
}

function DestinationForm({ data, set }: { data: DestinationData; set: (p: Record<string, unknown>) => void }) {
  const spec = DEST_TYPE_SPECS[data.destType];
  const settings = data.settings ?? {};
  const setField = (key: string, value: unknown) => set({ settings: { ...settings, [key]: value } });
  return (
    <Stack gap="xs">
      <Select
        label="Type"
        data={DEST_TYPES}
        value={data.destType}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => {
          const t = v ?? "rabbitmq";
          set({ destType: t, settings: defaultSettings(t) });
        }}
      />
      {spec?.fields.map((f) => renderField(f, settings[f.key], (v) => setField(f.key, v)))}
    </Stack>
  );
}

function IncomingRoutesSection({ destId }: { destId: string }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  const rows = destinationRoutes(destId, nodes, edges);
  return (
    <Stack gap="xs">
      <Divider />
      <Text fw={600} size="sm" tt="uppercase" c="dimmed">
        Incoming routes ({rows.length})
      </Text>
      {rows.length === 0 ? (
        <Text size="xs" c="dimmed">No endpoints route here yet.</Text>
      ) : (
        rows.map((r) => (
          <UnstyledButton
            key={r.edgeId}
            aria-label={`route from ${r.endpoint}: ${r.target || "(no target)"}`}
            onClick={() => setSelectedEdge(r.edgeId)}
          >
            <Group justify="space-between" wrap="nowrap" style={{ border: "1px solid #e6ebf5", borderRadius: 6, padding: "6px 8px" }}>
              <div style={{ minWidth: 0 }}>
                <Text size="xs" c="dimmed">from {r.endpoint}</Text>
                <Text size="sm" truncate>{r.target || "(no target)"}</Text>
              </div>
              <Text c="dimmed">›</Text>
            </Group>
          </UnstyledButton>
        ))
      )}
    </Stack>
  );
}

function RoutesSection({ endpointId }: { endpointId: string }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const setSelectedEdge = useGraphStore((s) => s.setSelectedEdge);
  const rows = endpointRoutes(endpointId, nodes, edges);
  return (
    <Stack gap="xs">
      <Divider />
      <Text fw={600} size="sm" tt="uppercase" c="dimmed">
        Routes ({rows.length})
      </Text>
      {rows.length === 0 ? (
        <Text size="xs" c="dimmed">Draw a link to a destination to add a route.</Text>
      ) : (
        rows.map((r) => (
          <UnstyledButton
            key={r.edgeId}
            aria-label={`route to ${r.dest}: ${r.target || "(no target)"}`}
            onClick={() => setSelectedEdge(r.edgeId)}
          >
            <Group justify="space-between" wrap="nowrap" style={{ border: "1px solid #e6ebf5", borderRadius: 6, padding: "6px 8px" }}>
              <div style={{ minWidth: 0 }}>
                <Text size="xs" c="dimmed">→ {r.dest}</Text>
                <Text size="sm" truncate>{r.target || "(no target)"}</Text>
              </div>
              <Text c="dimmed">›</Text>
            </Group>
          </UnstyledButton>
        ))
      )}
    </Stack>
  );
}
