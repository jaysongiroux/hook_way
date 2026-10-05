import { Alert, Stack, Text } from "@mantine/core";
import { useFieldDraft } from "./formDrafts";
import { JSONValidationEditor, type CoercionRow } from "../shared/ui/JSONValidationEditor";
import { validateJSONSample } from "../shared/wasm/jsonSample";

export function JSONValidationOptions({ value, onChange, scope, schemaField }: {
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
  scope: string;
  schemaField: "schema" | "json_schema";
}) {
  const [schemaDraft, saveSchemaDraft] = useFieldDraft(scope, schemaField);
  const [sampleDraft, saveSampleDraft] = useFieldDraft(scope, "_json_sample");
  const importedRules = Array.isArray(value.coercions) ? value.coercions : [];
  const coercions: CoercionRow[] = importedRules.map((rule) => {
    const record = rule && typeof rule === "object" ? rule as Record<string, unknown> : {};
    return { path: String(record.path ?? ""), type: String(record.type ?? "string") };
  });
  return (
    <Stack gap="sm">
      <Text size="sm">
        Check a parsed JSON event against a required schema. The node passes the same body onward when it validates; only listed conversions can change values. The original request bytes stay available for signature checks and raw forwarding.
      </Text>
      {value.coercions !== undefined && !Array.isArray(value.coercions) && (
        <Alert color="orange">Imported conversions are not a list. Edit or remove them before deployment.</Alert>
      )}
      <JSONValidationEditor
        schema={value[schemaField]}
        schemaFieldPath={schemaField}
        coercions={coercions}
        onSchemaChange={(schema) => onChange({ ...value, [schemaField]: schema })}
        onCoercionsChange={(next) => {
          const updated = { ...value };
          if (next.length) updated.coercions = next;
          else delete updated.coercions;
          onChange(updated);
        }}
        schemaDraft={schemaDraft}
        onSchemaDraftChange={saveSchemaDraft}
        sampleDraft={sampleDraft}
        onSampleDraftChange={saveSampleDraft}
        runSample={validateJSONSample}
      />
    </Stack>
  );
}
