import { Accordion, Anchor, Badge, Divider, Group, SegmentedControl, Select, Stack, Text, TextInput } from "@mantine/core";
import { useState } from "react";
import { useGraphStore } from "./store";
import type { DestinationData, EndpointData, FallbackData, LookupData, VariableData } from "./model";
import { VARIABLE_SOURCES, VARIABLE_KEY_SOURCES, LOOKUP_PROVIDERS, emptyLookup } from "./model";
import { BORDER } from "./InspectorSections";

const EMPTY_VARIABLE: VariableData = {
  name: "",
  source: "header",
  path: "",
  mode: "values",
  values: "",
  pattern: "",
  default: "",
};

// sourceFieldLabel names the "where do I read this from" field after the chosen
// source. A single label of "Lookup" collided with the `lookup` source type and
// read as if every variable involved a lookup provider.
function sourceFieldLabel(source: VariableData["source"]): string {
  switch (source) {
    case "header":
      return "Header name";
    case "body":
      return "Body path";
    case "query":
      return "Query key";
    case "path":
      return "Path segment";
    default:
      return "Key";
  }
}

// VariablesSection edits endpoints.<n>.variables and on_unresolved_variable.
// Each variable is one collapsed row (name plus source) so an endpoint with
// several of them stays readable; expanding a row reveals its fields. The
// fallback destination list excludes webhook and temporal destinations, which
// the validator rejects as fallbacks.
export function VariablesSection({
  data,
  set,
}: {
  data: EndpointData;
  set: (patch: Record<string, unknown>) => void;
}) {
  const nodes = useGraphStore((s) => s.nodes);
  const variables = data.variables ?? [];
  const fallback: FallbackData = data.fallback ?? { destination: "", target: "" };
  const [open, setOpen] = useState<string | null>(null);

  const setVariables = (next: VariableData[]) => set({ variables: next.length > 0 ? next : undefined });
  const update = (i: number, patch: Partial<VariableData>) =>
    setVariables(variables.map((v, idx) => (idx === i ? { ...v, ...patch } : v)));
  // A newly added variable opens immediately: it is empty, so there is nothing
  // to read on its collapsed row and every field still needs filling in.
  const add = () => {
    setOpen(`v${variables.length}`);
    setVariables([...variables, { ...EMPTY_VARIABLE }]);
  };
  const remove = (i: number) => {
    setOpen(null);
    setVariables(variables.filter((_, idx) => idx !== i));
  };

  const fallbackDests = nodes
    .filter((n) => n.data.kind === "destination")
    .map((n) => n.data as DestinationData)
    .filter((d) => d.destType !== "webhook" && d.destType !== "temporal")
    .map((d) => d.name);

  return (
    <Stack gap="xs">
      <Text size="xs" c="dimmed">
        Per-request values from the path, a header, the query string, or the body that fill {"{name}"} placeholders in
        route targets and webhook URLs.
      </Text>
      {variables.length > 0 && (
        <Accordion
          value={open}
          onChange={setOpen}
          variant="contained"
          radius="sm"
          chevronPosition="left"
          transitionDuration={0}
          styles={{
            item: { border: `1px solid ${BORDER}` },
            control: { paddingInline: 6, paddingBlock: 4 },
            content: { paddingInline: 6, paddingBottom: 6 },
          }}
        >
          {variables.map((v, i) => (
            <Accordion.Item key={i} value={`v${i}`}>
              <Accordion.Control>
                <Group justify="space-between" wrap="nowrap" gap={6}>
                  <Text size="xs" fw={500} truncate>
                    {v.name || "new variable"}
                  </Text>
                  <Badge size="xs" variant="light" color="gray" tt="none" style={{ flexShrink: 0 }}>
                    {v.source}
                  </Badge>
                </Group>
              </Accordion.Control>
              <Accordion.Panel>
                {open === `v${i}` ? (
                  <VariableFields v={v} onChange={(patch) => update(i, patch)} onRemove={() => remove(i)} />
                ) : null}
              </Accordion.Panel>
            </Accordion.Item>
          ))}
        </Accordion>
      )}
      <Anchor size="xs" onClick={add}>
        + Add variable
      </Anchor>
      {variables.length > 0 && (
        <>
          <Divider label="On unresolved variable" labelPosition="left" />
          <Select
            label="Fallback destination"
            size="xs"
            description="Receives events whose variables cannot be resolved; leave empty to reject them with 400"
            data={fallbackDests}
            value={fallback.destination || null}
            clearable
            comboboxProps={{ keepMounted: false }}
            onChange={(val) => set({ fallback: val ? { destination: val, target: fallback.target } : undefined })}
          />
          {fallback.destination && (
            <TextInput
              label="Fallback target"
              size="xs"
              placeholder="unrouted"
              value={fallback.target}
              onChange={(e) => set({ fallback: { ...fallback, target: e.currentTarget.value } })}
            />
          )}
        </>
      )}
    </Stack>
  );
}

// VariableFields edits one variable: its name, where its value comes from, and
// which values are accepted.
function VariableFields({
  v,
  onChange,
  onRemove,
}: {
  v: VariableData;
  onChange: (patch: Partial<VariableData>) => void;
  onRemove: () => void;
}) {
  return (
    <Stack gap={4}>
      <TextInput
        label="Variable name"
        size="xs"
        value={v.name}
        onChange={(e) => onChange({ name: e.currentTarget.value })}
      />
      <Select
        label="Source"
        size="xs"
        data={VARIABLE_SOURCES}
        value={v.source}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(val) => {
          const source = (val ?? "header") as VariableData["source"];
          const patch: Partial<VariableData> = { source };
          if (source === "lookup") {
            patch.lookup = v.lookup ?? emptyLookup();
            if (v.mode === "values" && !v.values) patch.mode = "any";
          } else if (v.mode === "any") {
            patch.mode = "values";
          }
          onChange(patch);
        }}
      />
      {v.source === "lookup" ? (
        <LookupFields
          lookup={v.lookup ?? emptyLookup()}
          set={(patch) => onChange({ lookup: { ...(v.lookup ?? emptyLookup()), ...patch } })}
        />
      ) : (
        <TextInput
          label={sourceFieldLabel(v.source)}
          size="xs"
          description="Defaults to the variable name"
          value={v.path}
          onChange={(e) => onChange({ path: e.currentTarget.value })}
        />
      )}
      <SegmentedControl
        fullWidth
        size="xs"
        data={
          v.source === "lookup"
            ? [
                { label: "Any value", value: "any" },
                { label: "Values", value: "values" },
                { label: "Pattern", value: "pattern" },
              ]
            : [
                { label: "Values", value: "values" },
                { label: "Pattern", value: "pattern" },
              ]
        }
        value={v.mode}
        onChange={(val) => onChange({ mode: val as VariableData["mode"] })}
      />
      {v.mode === "values" && (
        <TextInput
          label="Values (comma-separated)"
          size="xs"
          placeholder="qa-gm3, staging-portal, portal"
          value={v.values}
          onChange={(e) => onChange({ values: e.currentTarget.value })}
        />
      )}
      {v.mode === "pattern" && (
        <TextInput
          label="Pattern (anchored regex)"
          size="xs"
          placeholder="^qa-gm[0-9]+$"
          value={v.pattern}
          onChange={(e) => onChange({ pattern: e.currentTarget.value })}
        />
      )}
      <TextInput label="Default" size="xs" value={v.default} onChange={(e) => onChange({ default: e.currentTarget.value })} />
      <Anchor size="xs" c="red" onClick={onRemove}>
        Remove variable
      </Anchor>
    </Stack>
  );
}

// LookupFields edits a lookup-source variable: where the key comes from, the
// provider, and the provider's required fields. Timeout, cache, region, and
// headers round-trip untouched via lookup.extra (edit them in YAML).
function LookupFields({ lookup, set }: { lookup: LookupData; set: (patch: Partial<LookupData>) => void }) {
  return (
    <Stack gap={4}>
      <Select
        label="Key source"
        size="xs"
        data={VARIABLE_KEY_SOURCES}
        value={lookup.keySource}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(val) => set({ keySource: (val ?? "body") as LookupData["keySource"] })}
      />
      <TextInput
        label={sourceFieldLabel(lookup.keySource)}
        size="xs"
        placeholder="data.object.applicationId"
        value={lookup.keyPath}
        onChange={(e) => set({ keyPath: e.currentTarget.value })}
      />
      <Select
        label="Provider"
        size="xs"
        data={LOOKUP_PROVIDERS}
        value={lookup.provider}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(val) => set({ provider: (val ?? "dynamodb") as LookupData["provider"] })}
      />
      {lookup.provider === "dynamodb" && (
        <>
          <TextInput
            label="Table"
            size="xs"
            value={lookup.dynamodb.table}
            onChange={(e) => set({ dynamodb: { ...lookup.dynamodb, table: e.currentTarget.value } })}
          />
          <TextInput
            label="Index (optional)"
            size="xs"
            placeholder="CreatedAtIndex"
            value={lookup.dynamodb.index}
            onChange={(e) => set({ dynamodb: { ...lookup.dynamodb, index: e.currentTarget.value } })}
          />
          <TextInput
            label="Value attribute"
            size="xs"
            placeholder="env_name"
            value={lookup.dynamodb.value_attribute}
            onChange={(e) => set({ dynamodb: { ...lookup.dynamodb, value_attribute: e.currentTarget.value } })}
          />
          <Group grow gap={4}>
            <TextInput
              label="Filter attribute"
              size="xs"
              placeholder="id_type"
              value={lookup.dynamodb.filter_attribute}
              onChange={(e) => set({ dynamodb: { ...lookup.dynamodb, filter_attribute: e.currentTarget.value } })}
            />
            <TextInput
              label="Filter value"
              size="xs"
              placeholder="application"
              value={lookup.dynamodb.filter_value}
              onChange={(e) => set({ dynamodb: { ...lookup.dynamodb, filter_value: e.currentTarget.value } })}
            />
          </Group>
        </>
      )}
      {lookup.provider === "http" && (
        <>
          <TextInput
            label="URL (with {key})"
            size="xs"
            placeholder="https://resolver.internal/env?id={key}"
            description="{key} must sit in the path or query, never the host"
            value={lookup.http.url}
            onChange={(e) => set({ http: { ...lookup.http, url: e.currentTarget.value } })}
          />
          <Select
            label="Method"
            size="xs"
            data={["GET", "POST"]}
            value={lookup.http.method || "GET"}
            allowDeselect={false}
            comboboxProps={{ keepMounted: false }}
            onChange={(val) => set({ http: { ...lookup.http, method: val ?? "GET" } })}
          />
          <TextInput
            label="Value path"
            size="xs"
            placeholder="env_name"
            value={lookup.http.value_path}
            onChange={(e) => set({ http: { ...lookup.http, value_path: e.currentTarget.value } })}
          />
        </>
      )}
      {lookup.provider === "postgres" && (
        <>
          <TextInput
            label="Connection URL"
            size="xs"
            placeholder="${ROUTING_DB_URL}"
            value={lookup.postgres.url}
            onChange={(e) => set({ postgres: { ...lookup.postgres, url: e.currentTarget.value } })}
          />
          <TextInput
            label="Query (with {key})"
            size="xs"
            placeholder="SELECT env_name FROM routing WHERE id = {key}"
            value={lookup.postgres.query}
            onChange={(e) => set({ postgres: { ...lookup.postgres, query: e.currentTarget.value } })}
          />
        </>
      )}
    </Stack>
  );
}
