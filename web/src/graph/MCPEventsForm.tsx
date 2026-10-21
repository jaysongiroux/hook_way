import { useEffect, useRef } from "react";
import { Alert, Button, Group, Select, Stack, Text, Textarea } from "@mantine/core";
import { parseSchemaObject } from "../shared/ui/jsonPrecision";
import { object } from "./FormPresentation";
import { SchemaForm } from "./SchemaForm";
import { NODE_SCHEMAS, schemaProperties, type JSONSchema } from "./nodeSchema";
import { clearDraftBranch, useFieldDraft } from "./formDrafts";
import { useGraphStore } from "./store";

export function MCPEventsForm({ value, onChange, scope }: {
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
  scope?: string;
}) {
  const rawCatalog = useGraphStore((s) => s.configExtra.events);
  const catalog = object(rawCatalog);
  const events = Array.isArray(value.events) ? value.events.map(object) : [];
  const [saved, save] = useFieldDraft(scope, "events._rows");
  const ids = useRef<string[]>(saved?.rowIds ?? []);
  while (ids.current.length < events.length) ids.current.push(crypto.randomUUID());
  useEffect(() => {
    if (JSON.stringify(saved?.rowIds) !== JSON.stringify(ids.current))
      save({ rowIds: [...ids.current], error: "" });
  }, [events.length, saved?.rowIds, save]);
  const eventSchema = schemaProperties(NODE_SCHEMAS.mcp_trigger.schema).events;
  const item = typeof eventSchema === "object" ? eventSchema.items : undefined;
  const fields = schemaProperties(item);
  const update = (index: number, next: Record<string, unknown>) =>
    onChange({ ...value, events: events.map((event, i) => i === index ? next : event) });
  return <Stack gap="lg">
    <Text size="sm">Expose named events on this endpoint. Subscribers receive matching events through signed HTTPS callbacks. Configure durable delivery in Config → Events.</Text>
    {object(value.auth).no_auth === true && <Alert color="orange">Event subscriptions require API-key or OAuth authentication. Disable anonymous access before exposing events.</Alert>}
    {events.map((event, index) => {
      const eventScope = `${scope}/event/${ids.current[index]}`;
      const name = String(event.event ?? "");
      const names = [...new Set([...Object.keys(catalog), ...(name ? [name] : [])])];
      return <Stack key={ids.current[index]} gap="sm">
        <Group justify="space-between"><Text fw={600}>{name || "Event subscription"}</Text>
          <Button size="compact-xs" variant="subtle" color="red" onClick={() => {
            clearDraftBranch(eventScope); ids.current.splice(index, 1);
            save({ rowIds: [...ids.current], error: "" });
            onChange({ ...value, events: events.filter((_, i) => i !== index) });
          }}>Remove event subscription {index + 1}</Button>
        </Group>
        <Select label="Named event" data={names} value={name || null} searchable allowDeselect={false}
          description="Choose a definition from Config → Events."
          onChange={(next) => update(index, { ...event, event: next ?? "" })} />
        <SubscriptionSchema value={event.input_schema} scope={eventScope} onChange={(input_schema) => update(index, { ...event, input_schema })} />
        <Text fw={600} size="sm">Filter bindings</Text>
        <SchemaForm schema={fields.filters as JSONSchema} value={event.filters ?? {}}
          scope={eventScope} path="events.filters"
          onChange={(filters) => update(index, { ...event, filters })} />
        <Text size="xs" c="dimmed">Map every argument to a dotted field in event data. Scalar values match exactly; all supplied filters must match.</Text>
        <SchemaForm schema={{ type: "object", properties: Object.fromEntries(
          Object.entries(schemaProperties(fields.access)).filter(([key]) => ["api_keys", "scopes", "roles"].includes(key)),
        ) }} value={event.access ?? {}} scope={eventScope} path="events.access"
          labels={{ api_keys: "Allowed API keys", scopes: "Required scopes", roles: "Allowed roles" }}
          onChange={(access) => update(index, { ...event, access })} />
      </Stack>;
    })}
    <Button variant="light" disabled={Object.keys(catalog).every((name) => events.some((event) => event.event === name))} onClick={() => {
      const name = Object.keys(catalog).find((name) => !events.some((e) => e.event === name)) ?? Object.keys(catalog)[0];
      onChange({ ...value, events: [...events, { event: name,
        input_schema: { type: "object", additionalProperties: false }, filters: {}, access: {} }] });
    }}>Add event subscription</Button>
    {Object.keys(catalog).length === 0 && <Text size="sm" c="dimmed">Define an event in Config → Events first.</Text>}
  </Stack>;
}

function SubscriptionSchema({ value, scope, onChange }: { value: unknown; scope: string; onChange: (value: unknown) => void }) {
  const [draft, save] = useFieldDraft(scope, "events.input_schema");
  return <Stack gap={4}><Textarea data-json-editor="true" data-field-path="events.input_schema" label="Subscription argument schema" autosize minRows={5} maxRows={16}
    description="An object schema with scalar properties and additionalProperties: false."
    value={draft?.text ?? JSON.stringify(value ?? {}, null, 2)} error={draft?.error || undefined}
    onChange={(event) => {
      const text = event.currentTarget.value;
      try { const next = parseSchemaObject(text); if (next.type !== "object" || next.additionalProperties !== false) throw new Error("Use type: object and additionalProperties: false."); save({ text, error: "" }); onChange(next); }
      catch (error) { save({ text, error: error instanceof Error ? error.message : "Enter a JSON Schema object." }); }
    }} />
    {draft?.error && <Button size="compact-xs" variant="subtle" onClick={() => save(undefined)}>Revert subscription schema draft</Button>}
  </Stack>;
}
