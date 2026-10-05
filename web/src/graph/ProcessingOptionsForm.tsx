import type { ReactNode } from "react";
import {
  Alert,
  Button,
  Fieldset,
  NativeSelect,
  Stack,
  Text,
} from "@mantine/core";
import { BatchChildEditor } from "./BatchChildEditor";
import {
  asRecord,
  ConditionEditor,
  SwitchCasesEditor,
} from "./ConditionEditor";
import { clearDraftBranch } from "./formDrafts";
import { nodeFieldHelp, nodeFieldLabels } from "./help";
import {
  humanLabel,
  isSchemaObject,
  NODE_SCHEMAS,
  schemaEnum,
  schemaProperties,
  type JSONSchema,
} from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";
import { JSONValidationOptions } from "./JSONValidationOptions";

export interface ProcessingOptionsFormProps {
  nodeType: string;
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
  scope: string;
  nodeId?: string;
  renderNodeOptions?: (
    nodeType: string,
    value: Record<string, unknown>,
    onChange: (value: Record<string, unknown>) => void,
    scope: string,
  ) => ReactNode;
  onManageRouting?: () => void;
}
const TRANSFORM_FIELDS: Record<string, string[]> = {
  changed_fields: ["from", "to", "name"],
  changed_fields_js: ["from", "to", "name"],
  field_map: ["mappings"],
  xml_prune: ["rules"],
  xml_ordered_prune: ["xml_adapter"],
  json_schema: ["json_schema", "coercions"],
  strip_fields: ["fields"],
};
const TRANSFORM_LABELS: Record<string, string> = {
  changed_fields: "Compare changed fields",
  changed_fields_js: "Compare original JSON with JavaScript semantics",
  field_map: "Map fields",
  xml_prune: "Prune XML using built-in rules",
  xml_ordered_prune: "Prune XML using explicit bindings",
  json_schema: "Validate and coerce JSON",
  strip_fields: "Remove JSON fields",
};
const TRANSFORM_COPY: Record<string, string> = {
  changed_fields:
    "Compare two event values, such as body.before and body.after. Store the changed field names in a variable (default: changed_fields).",
  changed_fields_js:
    "Compare body paths in the original JSON request using JavaScript semantics. Both paths must begin with body.; transformed body values are not used.",
  field_map:
    "Copy values between event paths, such as body.id to vars.event_id. Add at least one source and destination pair.",
  xml_prune:
    "Apply the built-in XML pruning rules. Leave rules omitted to use the standard rule sequence.",
  xml_ordered_prune:
    "Bind every XML selector and the sequence attribute, then supply placeholder texts and optional item names. The pruning order is fixed.",
  json_schema:
    "Validate the decoded body against a nonempty JSON Schema (up to 256 KiB). Optional scalar coercions run before validation; external schema resources are disabled.",
  strip_fields: "Remove 1–64 dotted JSON paths from the decoded body.",
};

function choices(
  values: string[],
  current: unknown,
  labels?: Record<string, string>,
) {
  const value = current === undefined ? "" : String(current);
  return [
    ...(values.includes(value)
      ? []
      : [{ value, label: value ? `Imported: ${value}` : "Choose an option" }]),
    ...values.map((key) => ({
      value: key,
      label: labels?.[key] ?? humanLabel(key),
    })),
  ];
}
function withRequired(
  schema: JSONSchema | undefined,
  required: string[],
): JSONSchema {
  return isSchemaObject(schema)
    ? {
        ...schema,
        required: [...new Set([...(schema.required ?? []), ...required])],
      }
    : (schema ?? true);
}
function formSchema(nodeType: string, required: string[] = []) {
  const base = withRequired(NODE_SCHEMAS[nodeType]?.schema, required);
  if (!isSchemaObject(base)) return base;
  const properties = { ...schemaProperties(base) };
  for (const field of ["mappings", "fields"]) {
    if (isSchemaObject(properties[field]))
      properties[field] = { ...properties[field], minItems: 1 };
  }
  if (isSchemaObject(properties.xml_adapter)) {
    const adapter = properties.xml_adapter;
    properties.xml_adapter = {
      ...adapter,
      required: Object.keys(schemaProperties(adapter)),
      properties: Object.fromEntries(
        Object.entries(schemaProperties(adapter)).map(([key, child]) => [
          key,
          isSchemaObject(child) && child.type === "array"
            ? { ...child, minItems: 1 }
            : child,
        ]),
      ),
    };
  }
  return { ...base, properties };
}

function ImportNotice({
  value,
  allowed,
  known,
  scope,
  onChange,
}: {
  value: Record<string, unknown>;
  allowed: string[];
  known: string[];
  scope: string;
  onChange: (value: Record<string, unknown>) => void;
}) {
  const inactive = Object.keys(value).filter((key) => !allowed.includes(key));
  if (inactive.length === 0) return null;
  const unknown = inactive.filter((key) => !known.includes(key));
  return (
    <Alert color="yellow" title="Imported options need review">
      <Stack gap="xs">
        <Text size="sm">
          These fields are retained but do not belong to the selected mode:{" "}
          {inactive.join(", ")}.
          {unknown.length > 0 ? ` Unknown fields: ${unknown.join(", ")}.` : ""}{" "}
          Remove them explicitly or review the YAML before export.
        </Text>
        <Button
          size="xs"
          variant="light"
          color="yellow"
          onClick={() => {
            const next = { ...value };
            for (const key of inactive) {
              delete next[key];
              clearDraftBranch(scope, key);
            }
            onChange(next);
          }}
        >
          Remove inapplicable imported options
        </Button>
      </Stack>
    </Alert>
  );
}

export function ProcessingOptionsForm(props: ProcessingOptionsFormProps) {
  const { nodeType, value, onChange, scope } = props;
  if (nodeType === "transform") return <TransformForm {...props} />;
  if (nodeType === "envelope") return <EnvelopeForm {...props} />;
  if (nodeType === "batch") return <BatchForm {...props} />;
  const set = (key: string, next: unknown) =>
    onChange({ ...value, [key]: next });
  return (
    <Stack gap="sm">
      {nodeType === "filter" && (
        <>
          <Text size="sm">
            Every condition must match to continue. Choose whether a nonmatching
            event is dropped successfully or rejected.
          </Text>
          <ConditionEditor
            value={value.conditions}
            scope={`${scope}/conditions`}
            onChange={(next) => set("conditions", next)}
          />
          <SchemaForm
            schema={NODE_SCHEMAS.filter.schema}
            value={value}
            onChange={(next) => onChange(next as Record<string, unknown>)}
            scope={scope}
            hideFields={["conditions"]}
            order={["on_no_match", "drop_reason"]}
            defaults={{ on_no_match: "drop" }}
            labels={{
              on_no_match: "When conditions do not match",
              drop_reason: "Drop reason",
            }}
            fieldHelp={(field) => nodeFieldHelp(nodeType, field)}
          />
        </>
      )}
      {nodeType === "if_else" && (
        <>
          <NativeSelect
            label="Match conditions"
            aria-label="Match conditions"
            value={String(
              value.match === undefined || value.match === ""
                ? "all"
                : value.match,
            )}
            data={choices(
              ["all", "any"],
              value.match === undefined || value.match === ""
                ? "all"
                : value.match,
              { all: "All conditions", any: "Any condition" },
            )}
            description="All requires every condition; Any requires at least one. Both true and false output ports must be connected. Omitted match uses All."
            error={
              value.match === undefined ||
              value.match === "" ||
              value.match === "all" ||
              value.match === "any"
                ? undefined
                : "Choose All conditions or Any condition."
            }
            onChange={(event) => set("match", event.currentTarget.value)}
          />
          <ConditionEditor
            value={value.conditions}
            scope={`${scope}/conditions`}
            onChange={(next) => set("conditions", next)}
          />
        </>
      )}
      {nodeType === "switch" && (
        <>
          <SwitchCasesEditor
            value={value.cases}
            scope={`${scope}/cases`}
            onChange={(next) => set("cases", next)}
          />
          <SchemaForm
            schema={NODE_SCHEMAS.switch.schema}
            value={value}
            onChange={(next) => onChange(next as Record<string, unknown>)}
            scope={scope}
            hideFields={["cases"]}
            labels={{ default: "Default output port" }}
            fieldHelp={() =>
              "Optional fallback port when no case matches. Connect it if set; on_error is reserved."
            }
            errors={
              value.default === "on_error"
                ? { default: "on_error is reserved." }
                : undefined
            }
          />
        </>
      )}
    </Stack>
  );
}

function TransformForm({ value, onChange, scope }: ProcessingOptionsFormProps) {
  const kind = String(value.kind ?? "");
  const known = Object.keys(schemaProperties(NODE_SCHEMAS.transform.schema));
  const kinds =
    schemaEnum(schemaProperties(NODE_SCHEMAS.transform.schema).kind) ?? [];
  const active = TRANSFORM_FIELDS[kind] ?? [];
  const required =
    kind === "changed_fields" || kind === "changed_fields_js"
      ? ["from", "to"]
      : kind === "field_map"
        ? ["mappings"]
        : kind === "strip_fields"
          ? ["fields"]
          : kind === "json_schema"
            ? ["json_schema"]
            : kind === "xml_ordered_prune"
              ? ["xml_adapter"]
              : [];
  return (
    <Stack gap="sm">
      <NativeSelect
        label="Transform operation"
        aria-label="Transform operation"
        required
        value={kind}
        data={choices(kinds, value.kind, TRANSFORM_LABELS)}
        error={
          kinds.includes(kind)
            ? undefined
            : "Choose a supported transform operation."
        }
        description="Changing the operation removes fields belonging to another operation. Unknown imported fields remain available for review."
        onChange={(event) => {
          const nextKind = event.currentTarget.value;
          const next: Record<string, unknown> = { ...value, kind: nextKind };
          for (const key of known)
            if (key !== "kind" && !TRANSFORM_FIELDS[nextKind]?.includes(key)) {
              delete next[key];
              clearDraftBranch(scope, key);
            }
          onChange(next);
        }}
      />
      {TRANSFORM_COPY[kind] && <Text size="sm">{TRANSFORM_COPY[kind]}</Text>}
      <ImportNotice
        value={value}
        allowed={["kind", ...active]}
        known={known}
        scope={scope}
        onChange={onChange}
      />
      <SchemaForm
        schema={formSchema("transform", required)}
        labels={nodeFieldLabels("transform")}
        value={value}
        onChange={(next) => onChange(next as Record<string, unknown>)}
        scope={scope}
        hideFields={known.filter((key) => !active.includes(key) || (kind === "json_schema" && ["json_schema", "coercions"].includes(key)))}
        order={active}
        fieldHelp={(field) => nodeFieldHelp("transform", field)}
        defaults={
          kind.startsWith("changed_fields")
            ? { name: "changed_fields" }
            : undefined
        }
      />
      {kind === "json_schema" && (
        <JSONValidationOptions value={value} onChange={onChange} scope={scope} schemaField="json_schema" />
      )}
    </Stack>
  );
}

function EnvelopeForm({ value, onChange, scope }: ProcessingOptionsFormProps) {
  const format = String(
    value.body_format === undefined || value.body_format === ""
      ? "json"
      : value.body_format,
  );
  const formats =
    schemaEnum(schemaProperties(NODE_SCHEMAS.envelope.schema).body_format) ??
    [];
  const active =
    format === "xml" ? "xml" : format === "multipart" ? "multipart" : undefined;
  const parser = asRecord(active ? value[active] : undefined);
  return (
    <Stack gap="sm">
      <NativeSelect
        label="Body format"
        aria-label="Body format"
        value={format}
        data={choices(formats, format, {
          json: "JSON",
          xml: "XML",
          raw: "Raw bytes",
          multipart: "Multipart form data",
        })}
        description="Omitted format uses JSON. Changing format removes the other parser's options."
        error={
          formats.includes(format)
            ? undefined
            : "Choose a supported body format."
        }
        onChange={(event) => {
          const nextFormat = event.currentTarget.value;
          const next: Record<string, unknown> = {
            ...value,
            body_format: nextFormat,
          };
          for (const key of ["xml", "multipart"])
            if (key !== nextFormat) {
              delete next[key];
              clearDraftBranch(scope, key);
            }
          onChange(next);
        }}
      />
      <ImportNotice
        value={value}
        allowed={["body_format", ...(active ? [active] : [])]}
        known={["body_format", "xml", "multipart"]}
        scope={scope}
        onChange={onChange}
      />
      <Text size="sm">
        {format === "raw"
          ? "Keep the original request bytes without decoding a body object."
          : format === "json"
            ? "Decode the body as JSON. The read-body limit caps the request."
            : "Parser limits apply after the enforced read-body limit; the smaller limit determines what can be accepted. Sizes below are bytes."}
      </Text>
      {active && (
        <Fieldset
          legend={`${active === "xml" ? "XML" : "Multipart"} parser limits`}
          p="sm"
        >
          <Stack gap="xs">
            {value[active] !== undefined && !parser && (
              <Alert color="red">
                The imported parser options are not an object and are retained.
                Edit a parser field or reset its limits to replace them.
              </Alert>
            )}
            <SchemaForm
              schema={schemaProperties(NODE_SCHEMAS.envelope.schema)[active]}
              value={parser ?? {}}
              scope={scope}
              path={active}
              onChange={(next) => onChange({ ...value, [active]: next })}
              fieldHelp={(field) => nodeFieldHelp("envelope", field)}
              labels={{
                max_bytes: "Maximum XML bytes",
                max_depth: "Maximum element depth",
                max_nodes: "Maximum XML nodes",
                max_total_bytes: "Maximum total multipart bytes",
                max_parts: "Maximum parts",
                max_part_bytes: "Maximum bytes per part",
                include_content: "Include file content",
              }}
              defaults={
                active === "multipart"
                  ? {
                      "multipart.include_content": true,
                      "multipart.max_parts": 16,
                      "multipart.max_part_bytes": 1048576,
                    }
                  : {
                      "xml.max_bytes": 1048576,
                      "xml.max_depth": 256,
                      "xml.max_nodes": 100000,
                    }
              }
            />
            {value[active] !== undefined && (
              <Button
                size="xs"
                variant="subtle"
                onClick={() => {
                  const next = { ...value };
                  delete next[active];
                  clearDraftBranch(scope, active);
                  onChange(next);
                }}
              >
                Use default {active} parser limits
              </Button>
            )}
          </Stack>
        </Fieldset>
      )}
    </Stack>
  );
}

function validBatchTimeout(value: unknown): boolean {
  if (value === undefined || value === "") return true;
  if (typeof value !== "string") return false;
  const text = value.startsWith("+") ? value.slice(1) : value;
  const pieces = [
    ...text.matchAll(/((?:\d+(?:\.\d*)?|\.\d+))(ns|us|µs|μs|ms|s|m|h)/g),
  ];
  if (pieces.length === 0 || pieces.map((piece) => piece[0]).join("") !== text)
    return false;
  const units: Record<string, number> = {
    ns: 0.000001,
    us: 0.001,
    µs: 0.001,
    μs: 0.001,
    ms: 1,
    s: 1000,
    m: 60000,
    h: 3600000,
  };
  const duration = pieces.reduce(
    (total, piece) => total + Number(piece[1]) * units[piece[2]],
    0,
  );
  return Number.isFinite(duration) && duration > 0 && duration <= 120000;
}

function BatchForm({
  value,
  onChange,
  scope,
  nodeId,
  renderNodeOptions,
  onManageRouting,
}: ProcessingOptionsFormProps) {
  const configExtra = useGraphStore((state) => state.configExtra);
  const routing = asRecord(configExtra.routing) ?? {};
  const profiles = asRecord(routing.profiles) ?? {};
  const environments = asRecord(routing.environments) ?? {};
  const mode = String(value.mode ?? "");
  const profile = String(value.profile ?? "");
  const admitted = Object.entries(environments).flatMap(([name, raw]) => {
    const env = asRecord(raw);
    const selected = asRecord(profiles[profile]);
    if (
      !env ||
      !Array.isArray(env.profiles) ||
      !env.profiles.includes(profile) ||
      !Array.isArray(env.accounts) ||
      !env.accounts.includes(selected?.account_id)
    )
      return [];
    const aliases = Array.isArray(env.aliases)
      ? env.aliases.filter((item): item is string => typeof item === "string")
      : [];
    return [name, ...aliases];
  });
  const known = Object.keys(schemaProperties(NODE_SCHEMAS.batch.schema));
  const active = known.filter((key) =>
    mode === "shared"
      ? key !== "receiving_environment" && key !== "concurrency"
      : mode === "dedicated"
        ? key !== "group_id_path"
        : true,
  );
  const incompatible =
    mode === "shared" &&
    value.concurrency !== undefined &&
    value.concurrency !== 1;
  const display =
    mode === "shared" && value.concurrency === 1 ? { ...value } : value;
  if (display !== value) delete display.concurrency;
  return (
    <Stack gap="sm">
      <NativeSelect
        label="Batch mode"
        aria-label="Batch mode"
        required
        value={mode}
        data={choices(["shared", "dedicated"], mode, {
          shared: "Shared — sequential children",
          dedicated: "Dedicated — one receiving environment",
        })}
        description="Shared accepts an object or array and processes children sequentially. Dedicated accepts an array, skips other environments, and can process children concurrently."
        error={
          ["shared", "dedicated"].includes(mode)
            ? undefined
            : "Choose a batch mode."
        }
        onChange={(event) => {
          const selected = event.currentTarget.value;
          const next: Record<string, unknown> = { ...value, mode: selected };
          for (const key of selected === "shared"
            ? ["receiving_environment", "concurrency"]
            : ["group_id_path"]) {
            delete next[key];
            clearDraftBranch(scope, key);
          }
          onChange(next);
        }}
      />
      <ImportNotice
        value={display}
        allowed={active}
        known={known}
        scope={scope}
        onChange={(next) =>
          onChange({
            ...next,
            ...(mode === "shared" && value.concurrency === 1
              ? { concurrency: 1 }
              : {}),
          })
        }
      />
      {incompatible && (
        <Text c="red" size="sm">
          Shared batches require concurrency 1. Choose Shared again or remove
          the inapplicable imported options.
        </Text>
      )}
      <NativeSelect
        label="Routing profile"
        aria-label="Routing profile"
        required
        value={profile}
        data={choices(Object.keys(profiles), value.profile)}
        error={
          profile && profiles[profile]
            ? undefined
            : "Create and select a routing profile."
        }
        onChange={(event) =>
          onChange({ ...value, profile: event.currentTarget.value })
        }
      />
      {Object.keys(profiles).length === 0 && (
        <Text size="sm">
          Add a routing profile and its allowed environments in Config → Routing
          before completing this batch.
        </Text>
      )}
      {onManageRouting && (
        <Button variant="subtle" size="xs" onClick={onManageRouting}>
          Manage routing profiles and environments
        </Button>
      )}
      {mode === "dedicated" && (
        <NativeSelect
          label="Receiving environment"
          aria-label="Receiving environment"
          required
          value={String(value.receiving_environment ?? "")}
          data={choices(admitted, value.receiving_environment)}
          description="Only environments allowed by this profile and account are listed. Events for other environments are skipped."
          error={
            admitted.includes(String(value.receiving_environment))
              ? undefined
              : "Select an environment allowed by this profile and account."
          }
          onChange={(event) =>
            onChange({
              ...value,
              receiving_environment: event.currentTarget.value,
            })
          }
        />
      )}
      <SchemaForm
        schema={formSchema("batch")}
        labels={nodeFieldLabels("batch")}
        value={value}
        scope={scope}
        onChange={(next) => onChange(next as Record<string, unknown>)}
        hideFields={[
          "mode",
          "profile",
          "receiving_environment",
          "child",
          ...(mode === "shared"
            ? ["concurrency"]
            : mode === "dedicated"
              ? ["group_id_path"]
              : []),
        ]}
        order={[
          "environment_path",
          "event_id_path",
          "group_id_path",
          "max_children",
          "concurrency",
          "max_inflight",
          "timeout",
        ]}
        fieldHelp={(field) => nodeFieldHelp("batch", field)}
        defaults={{
          max_children: 1000,
          concurrency: mode === "shared" ? 1 : 8,
          max_inflight: 64,
          timeout: "30s",
        }}
        errors={
          !validBatchTimeout(value.timeout)
            ? {
                timeout:
                  "Enter a positive duration no longer than 2m, such as 30s.",
              }
            : undefined
        }
      />
      <Fieldset legend="Child flow" p="sm">
        <BatchChildEditor
          value={value.child}
          scope={`${scope}/child`}
          parentId={nodeId}
          onChange={(next) => onChange({ ...value, child: next })}
          renderNodeOptions={renderNodeOptions}
        />
      </Fieldset>
    </Stack>
  );
}
