import { Alert, Button, Modal, Stack, Text, Textarea, Switch, Divider } from "@mantine/core";
import { useBuilderValidation } from "./BuilderValidation";
import { useFieldDraft } from "./formDrafts";
import { SchemaForm } from "./SchemaForm";
import { schemaProperties, type JSONSchema } from "./nodeSchema";
import { useGraphStore } from "./store";
import { credentialNameError, record, WorkspaceEntries } from "./WorkspaceEntries";
import { parseSchemaObject } from "../shared/ui/jsonPrecision";
import catalog from "./workspaceSchema.json";

const definition = catalog.events.additionalProperties as JSONSchema;

export function EventsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const value = useGraphStore((s) => s.configExtra.events);
  const delivery = useGraphStore((s) => s.configExtra.event_delivery);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  return (
    <Modal opened={opened} onClose={onClose} title="Events" size="xl"
      closeButtonProps={{ "aria-label": "Close Events" }}>
      <Stack gap="md">
        <Text size="sm">
          Define the JSON payload your flows create. Use Create event to validate
          and wrap a payload, then Publish to send it to an existing destination.
        </Text>
        <Switch aria-label="Enable durable MCP delivery" label="Enable durable MCP delivery" checked={Boolean(delivery)}
          description="Requires PostgreSQL and a stable encryption key on your deployed server."
          onChange={(event) => setConfigExtra({ event_delivery: event.currentTarget.checked ? { postgres_url: "${EVENT_DATABASE_URL}", namespace: "agent-events", encryption_key: { provider: "env", name: "EVENT_ENCRYPTION_KEY" } } : undefined })} />
        {Boolean(delivery) && <SchemaForm schema={catalog.event_delivery as JSONSchema} value={delivery}
          scope="event_delivery" path="event_delivery" groupObjects
          labels={{ postgres_url: "PostgreSQL URL", namespace: "Deployment namespace", encryption_key: "Encryption key reference" }}
          onChange={(next) => setConfigExtra({ event_delivery: next })} />}
        <Divider />
        <WorkspaceEntries label="event" value={value} nameError={credentialNameError}
          initial={() => ({ description: "", payload_schema: { type: "object" } })}
          onChange={(events) => setConfigExtra({ events })}
          emptyMessage="No events defined. Add a name such as issue.opened and describe its payload.">
          {(name, entry, change) => <EventDefinitionEditor key={name} name={name} entry={entry} onChange={change} />}
        </WorkspaceEntries>
      </Stack>
    </Modal>
  );
}

function EventDefinitionEditor({ name, entry, onChange }: {
  name: string;
  entry: Record<string, unknown>;
  onChange: (entry: Record<string, unknown>) => void;
}) {
  const scope = `events/${encodeURIComponent(name)}`;
  const path = `events.${name}.payload_schema`;
  const [draft, saveDraft] = useFieldDraft(scope, path);
  const validation = useBuilderValidation();
  const issues = validation.issues.filter((issue) => issue.field === `events.${name}` || issue.field.startsWith(`events.${name}.`));
  const serialized = JSON.stringify(entry.payload_schema ?? {}, null, 2);
  const unknown = Object.keys(entry).filter((key) => !["description", "payload_schema"].includes(key));
  const schema = schemaProperties(definition);
  return (
    <Stack gap="sm">
      {issues.length > 0 && <Alert color="red" title="Event definition needs attention">{issues.map((issue) => <Text size="sm" key={issue.field + issue.message}>{issue.field}: {issue.message}</Text>)}</Alert>}
      {unknown.length > 0 && <Alert color="orange">Unrecognized settings are retained: {unknown.join(", ")}. Remove them from the YAML before deployment.</Alert>}
      <SchemaForm schema={{ type: "object", properties: { description: schema.description }, required: ["description"] }}
        value={entry} onChange={(next) => onChange(record(next))}
        scope={scope} path={`events.${name}`} labels={{ description: "Description" }}
        fieldHelp={() => "Explain when this event is created and what its payload contains."} />
      <Textarea label="Payload schema" data-field-path={path} data-json-editor="true"
        description="JSON Schema with type: object, up to 64 KiB. Only local references are allowed. Values are not converted and defaults are not inserted."
        value={draft?.text ?? serialized} error={draft?.error || undefined}
        autosize minRows={8} maxRows={20}
        onChange={(event) => {
          const text = event.currentTarget.value;
          try {
            const payload = parseSchemaObject(text);
            if (payload.type !== "object") throw new Error("Payload schema must declare type: object.");
            saveDraft({ text, error: "" });
            onChange({ ...entry, payload_schema: payload });
          } catch (error) {
            saveDraft({ text, error: error instanceof SyntaxError
              ? "Enter valid JSON. Your draft is retained until corrected or reverted."
              : error instanceof Error ? error.message : "Enter a JSON Schema object." });
          }
        }} />
      {draft?.error && <Button size="compact-xs" variant="subtle" onClick={() => saveDraft(undefined)}>Revert payload schema draft</Button>}
      <Text size="xs" c="dimmed">Schema validation runs locally. Event creation and delivery run on your deployed Hookway server.</Text>
    </Stack>
  );
}
