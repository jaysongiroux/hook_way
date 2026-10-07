import { Button, Group, Stack, Text, Title } from "@mantine/core";
import type { components } from "../shared/contracts/generated/replay";

export type Inspection = components["schemas"]["InspectionPayload"];
// Decode only the wrapper. The body remains text to preserve number precision
// and ensure operator-supplied HTML/XML cannot execute in the browser.
export function decodeInspection(text: string): Inspection {
  const value = JSON.parse(text) as Inspection;
  if (value?.version !== 1)
    throw new Error("This inspection payload version is not supported. Download the inspection JSON to view it.");
  const body = value.body;
  const headers = value.headers;
  const variables = value.variables;
  if (!body || typeof body.content !== "string" || typeof body.content_type !== "string" || !["utf8", "base64"].includes(body.encoding) || typeof value.method !== "string" || typeof value.path !== "string" ||
      (headers !== null && (typeof headers !== "object" || Array.isArray(headers) || Object.values(headers).some((v) => v !== null && (!Array.isArray(v) || v.some((h) => typeof h !== "string"))))) ||
      (variables !== null && (typeof variables !== "object" || Array.isArray(variables) || Object.values(variables).some((v) => typeof v !== "string"))))
    throw new Error("The inspection payload is invalid. Download it to investigate the captured artifact.");
  return value;
}

const BODY_PREVIEW_BYTES = 64 * 1024;
export function InspectionPreview({ value, onClose }: { value: Inspection; onClose: () => void }) {
  const bytes = new TextEncoder().encode(value.body.content);
  const clipped = bytes.length > BODY_PREVIEW_BYTES;
  const body = new TextDecoder().decode(bytes.subarray(0, BODY_PREVIEW_BYTES), { stream: clipped });
  return (
    <Stack gap="lg" mt="md" role="region" aria-label="Captured inspection">
      <section>
        <Group justify="space-between" mb="xs">
          <Title order={3} size="h5">Current body</Title>
          <Button size="xs" variant="subtle" onClick={onClose}>Close preview</Button>
        </Group>
        <Text size="xs" c="dimmed">{value.body.content_type || "Content type not recorded"} · {value.body.encoding === "base64" ? "Base64 · binary body" : "UTF-8 text"}</Text>
        {clipped && <Text size="sm" mt="xs">Showing the first 64 KiB of body text. Download the inspection JSON for the full body.</Text>}
        {body ? <pre className="payload-preview">{body}</pre> : <Text size="sm" mt="sm" c="dimmed">The captured body is empty.</Text>}
      </section>
      <section aria-label="Request context">
        <Title order={3} size="h5" mb="sm">Request context</Title>
        <dl className="delivery-metadata">
          <div><dt>Method</dt><dd>{value.method || "Not recorded"}</dd></div>
          <div><dt>Path</dt><dd className="identifier">{value.path || "Not recorded"}</dd></div>
        </dl>
      </section>
      <ContextValues title="Headers" values={Object.entries(value.headers || {}).map(([key, values]) => [key, (values || []).join("\n")])} empty="No headers were retained by the capture policy." />
      <ContextValues title="Variables" values={Object.entries(value.variables || {})} empty="No variables were set at inspection time." />
    </Stack>
  );
}
function ContextValues({ title, values, empty }: { title: string; values: [string, string][]; empty: string }) {
  return (
    <section aria-label={title}>
      <Title order={3} size="h5" mb="sm">{title}</Title>
      {values.length ? <dl className="inspection-context">{values.map(([key, value]) => <div key={key}><dt className="identifier">{key}</dt><dd>{value || "(empty)"}</dd></div>)}</dl> : <Text size="sm" c="dimmed">{empty}</Text>}
    </section>
  );
}
