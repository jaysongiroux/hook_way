import { editorAPIFallbackEnabled } from "../wasm/validationMode";
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Code, Group, NativeSelect, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type { JSONSampleInput, JSONSampleResult, ScalarCoercion } from "../wasm/jsonSample";
import { parseSchemaObject, precisionRisk } from "./jsonPrecision";

export interface TextDraft { text?: string; error: string }
export interface CoercionRow { path: string; type: string }

const TYPES = ["string", "number", "integer", "boolean", "null"];
function supportedRule(rule: CoercionRow): rule is ScalarCoercion {
  return TYPES.some((type) => type === rule.type);
}
function ruleError(rule: CoercionRow, schema: unknown): string | undefined {
  if (!rule.path || rule.path.length > 256 || rule.path.split(".").some((part) => !part)) {
    return "Enter a dotted property path (up to 256 characters).";
  }
  let current: unknown = schema;
  for (const part of rule.path.split(".")) {
    if (!current || typeof current !== "object" || Array.isArray(current)) return "Path must refer to a schema property.";
    const properties = (current as Record<string, unknown>).properties;
    if (!properties || typeof properties !== "object" || Array.isArray(properties)) return "Path must refer to a schema property.";
    current = (properties as Record<string, unknown>)[part];
  }
  if (!current || typeof current !== "object" || Array.isArray(current) ||
    (current as Record<string, unknown>).type !== rule.type) {
    return "Conversion type must match one declared property type.";
  }
  return undefined;
}

export function JSONValidationEditor({ schema, coercions, onSchemaChange, onCoercionsChange,
  schemaDraft, onSchemaDraftChange, sampleDraft, onSampleDraftChange, runSample, schemaFieldPath = "schema",
}: {
  schema: unknown;
  coercions: CoercionRow[];
  onSchemaChange: (schema: Record<string, unknown>) => void;
  onCoercionsChange: (coercions: CoercionRow[]) => void;
  schemaDraft?: TextDraft;
  onSchemaDraftChange: (draft: TextDraft | undefined) => void;
  sampleDraft?: TextDraft;
  onSampleDraftChange: (draft: TextDraft | undefined) => void;
  runSample: (input: JSONSampleInput) => Promise<JSONSampleResult | null>;
  schemaFieldPath?: string;
}) {
  const serialized = JSON.stringify(schema ?? {}, null, 2);
  const [schemaText, setSchemaText] = useState(schemaDraft?.text ?? serialized);
  const [schemaError, setSchemaError] = useState(schemaDraft?.error ?? (serialized === "{}" ? "Enter a nonempty JSON Schema object." : precisionRisk(serialized) ?? ""));
  const [sample, setSample] = useState(sampleDraft?.text ?? "");
  const [result, setResult] = useState<JSONSampleResult | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [running, setRunning] = useState(false);
  const lastCommitted = useRef(serialized);
  const requestId = useRef(0);

  useEffect(() => () => { requestId.current++; }, []);
  const invalidateResult = () => {
    requestId.current++;
    setResult(null);
    setUnavailable(false);
    setRunning(false);
  };

  useEffect(() => {
    if (lastCommitted.current === serialized) return;
    lastCommitted.current = serialized;
    if (schemaDraft?.error) return;
    invalidateResult();
    setSchemaText(serialized);
    setSchemaError(serialized === "{}" ? "Enter a nonempty JSON Schema object." : precisionRisk(serialized) ?? "");
  }, [serialized, schemaDraft?.error]);

  const updateSchema = (text: string) => {
    setSchemaText(text);
    invalidateResult();
    try {
      const parsed = parseSchemaObject(text);
      setSchemaError("");
      onSchemaDraftChange({ text, error: "" });
      lastCommitted.current = JSON.stringify(parsed, null, 2);
      onSchemaChange(parsed);
    } catch (error) {
      const message = error instanceof SyntaxError
        ? "Enter valid JSON. This schema draft is retained until corrected or reverted."
        : error instanceof Error ? error.message : "Enter a JSON Schema object.";
      setSchemaError(message);
      onSchemaDraftChange({ text, error: message });
    }
  };
  const editRule = (index: number, change: Partial<CoercionRow>) => {
    const next = coercions.map((rule, i) => i === index ? { ...rule, ...change } : rule);
    onCoercionsChange(next);
    invalidateResult();
  };
  const run = async () => {
    const currentRequest = ++requestId.current;
    setUnavailable(false);
    setRunning(true);
    setResult(null);
    try {
      const safeSchema = parseSchemaObject(schemaText);
      if (!coercions.every(supportedRule)) {
        setResult({ valid: false, issues: [{ field: "coercions", path: "", keyword: "enum", message: "Choose a supported conversion type." }] });
        return;
      }
      const response = await runSample({ schema: safeSchema, coercions, sample });
      if (requestId.current !== currentRequest) return;
      if (response) setResult(response);
      else setUnavailable(true);
    } catch {
      if (requestId.current === currentRequest) setUnavailable(true);
    } finally {
      if (requestId.current === currentRequest) setRunning(false);
    }
  };
  return (
    <Stack gap="md">
      <div>
        <Text size="sm" fw={600}>JSON Schema</Text>
        <Text size="xs" c="dimmed">
          Required, nonempty schema. Draft 7 is the default. Local $ref and formats are checked; external references are unavailable. Validation is strict: defaults are not inserted and extra fields are not removed.
        </Text>
      </div>
      <Textarea
        aria-label="JSON Schema" data-field-path={schemaFieldPath} data-json-editor="true"
        value={schemaText} onChange={(event) => updateSchema(event.currentTarget.value)}
        error={schemaError || undefined} autosize minRows={8} maxRows={20}
        styles={{ input: { fontFamily: "monospace" } }}
      />
      {schemaError && <Button size="compact-xs" variant="subtle" onClick={() => {
        onSchemaDraftChange(undefined);
        setSchemaText(serialized);
        setSchemaError(serialized === "{}" ? "Enter a nonempty JSON Schema object." : precisionRisk(serialized) ?? "");
      }}>Revert schema draft</Button>}
      <div>
        <Text size="sm" fw={600}>Convert values before validation</Text>
        <Text size="xs" c="dimmed">
          Optional. Each path must name a property whose schema declares exactly one matching type. Values not listed here keep their original type.
        </Text>
      </div>
      {coercions.map((rule, index) => (
        <Group key={index} align="flex-end" wrap="wrap" gap="xs">
          <TextInput
            label={`Property path ${index + 1}`} aria-label={`Property path ${index + 1}`}
            value={rule.path} placeholder="customer.id" style={{ flex: "1 1 180px", minWidth: 0 }}
            error={!schemaError ? ruleError(rule, schema) : undefined}
            onChange={(event) => editRule(index, { path: event.currentTarget.value })}
          />
          <NativeSelect
            label={`Convert to ${index + 1}`} aria-label={`Convert to ${index + 1}`}
            value={rule.type} data={TYPES.includes(rule.type) ? TYPES : [{ value: rule.type, label: `Imported: ${rule.type}` }, ...TYPES.map((type) => ({ value: type, label: type }))]} style={{ flex: "0 1 130px", minWidth: 110 }}
            onChange={(event) => editRule(index, { type: event.currentTarget.value })}
          />
          <Button aria-label={`Remove conversion ${index + 1}`} variant="subtle" color="red" onClick={() => { onCoercionsChange(coercions.filter((_, i) => i !== index)); invalidateResult(); }}>
            Remove
          </Button>
        </Group>
      ))}
      <Button variant="light" size="xs" disabled={coercions.length >= 64}
        onClick={() => { onCoercionsChange([...coercions, { path: "", type: "string" }]); invalidateResult(); }}>
        Add conversion
      </Button>
      <div>
        <Text size="sm" fw={600}>Try a JSON sample</Text>
        <Text size="xs" c="dimmed">Check the schema against one event body. The sample stays in this editor; it is not exported as runtime config. Numbers are sent as typed text.</Text>
      </div>
      <Textarea
        aria-label="JSON sample" data-field-path="sample" value={sample}
        onChange={(event) => { const next = event.currentTarget.value; setSample(next); onSampleDraftChange({ text: next, error: "" }); invalidateResult(); }}
        autosize minRows={5} maxRows={16} placeholder='{"customer":{"id":"42"}}'
        styles={{ input: { fontFamily: "monospace" } }}
      />
      <Button onClick={run} loading={running} disabled={!sample.trim() || !!schemaError}>Validate sample</Button>
      {unavailable && <Alert color="orange" title="Sample validation unavailable">
        {editorAPIFallbackEnabled() ? "The local validator and editor API could not check this sample. Try again when one is available." : "The local validator could not check this sample. Retry local validation in the builder, then run the sample again."}
      </Alert>}
      {result && (result.valid ? (
        <Alert color="green" title="Sample is valid">
          {result.output !== undefined && <><Text size="xs">Validated body</Text><Code block style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{result.output}</Code></>}
        </Alert>
      ) : (
        <Alert color="red" title="Sample did not validate">
          <Stack gap="xs">{result.issues.map((issue, index) => (
            <Text key={index} size="sm">{issue.path || issue.field}: {issue.message}</Text>
          ))}</Stack>
        </Alert>
      ))}
    </Stack>
  );
}
