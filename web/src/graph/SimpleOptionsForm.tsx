import { NODE_DESCRIPTORS, nodeDescriptor } from "./nodeDescriptors";
import { useState } from "react";
import {
  Alert,
  Button,
  Code,
  Group,
  MultiSelect,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { useGraphStore } from "./store";
import { record } from "./WorkspaceEntries";
import {
  Fields,
  ReferenceSelect,
  InactiveSettings,
  changeMode,
  type OptionsProps,
} from "./FormPresentation";
import { Sections } from "./InspectorSections";
import { NODE_SCHEMAS, schemaProperties, schemaRequired } from "./nodeSchema";
import { goDurationIssue } from "./goDuration";
import { DARK } from "../tokens";

export const SIMPLE_NODE_TYPES = Object.entries(NODE_DESCRIPTORS)
  .filter(([, descriptor]) => descriptor.editor === "simple")
  .map(([type]) => type);

export const CODE_EXAMPLE = "return { body: input.body, vars: input.vars };";

export function SimpleOptionsForm(props: OptionsProps) {
  const { nodeType, value, onChange, nodeId } = props;
  const events = useGraphStore((s) => s.configExtra.events);
  const providers = useGraphStore((s) => s.configExtra.lookups);
  const meta = NODE_SCHEMAS[nodeType];
  if (!meta)
    return (
      <Alert color="orange">
        This node type is not in the runtime catalog. Imported options are
        retained; choose a supported node type before deploying.
      </Alert>
    );
  const presentation = nodeDescriptor(nodeType)?.simple;
  const keys = Object.keys(schemaProperties(meta.schema));
  const visible = presentation?.primaryFields ?? keys;
  const managed =
    nodeType === "http_trigger"
      ? ["methods"]
      : nodeType === "event"
        ? ["event"]
      : nodeType === "lookup"
          ? ["provider", "table", "cache", "negative_ttl"]
          : [];
  const secondary = keys.filter(
    (key) => !visible.includes(key) && !managed.includes(key),
  );
  const required = schemaRequired(meta.schema);
  const errors: Record<string, string> = {};
  for (const key of ["ttl", "timeout", "negative_ttl"]) {
    if (keys.includes(key)) {
      const issue = goDurationIssue(value[key], {
        allowZero: key === "negative_ttl",
      });
      if (issue) errors[key] = issue;
    }
  }
  if (value.cache && typeof value.cache === "object") {
    const issue = goDurationIssue((value.cache as Record<string, unknown>).ttl);
    if (issue) errors["cache.ttl"] = issue;
  }
  const render = (fields: string[]) => (
    <Fields
      {...props}
      fields={fields}
      required={required.filter((key) => fields.includes(key))}
      errors={errors}
      labels={{
        path: "Request path",
        max_bytes: "Body read limit (bytes)",
        cidrs: "Allowed IPs and networks",
        max_hops: "Maximum forwarded hops",
        ttl: "Cache lifetime",
        key: nodeType === "lookup" ? "Lookup key" : "Request key template",
        provider: "Lookup provider",
        event_id_path: "Event ID path",
        name: "Result variable",
        script: "JavaScript function body",
        rpm: "Requests per minute",
        burst: "Burst allowance",
        max_keys: "Maximum tracked identities",
      }}
      fieldHelp={(path) =>
        presentation?.fieldHelp?.[path] ??
        presentation?.fieldHelp?.[path.split(".").at(-1) ?? path]
      }
      groupObjects
    />
  );
  return (
    <Stack gap="sm">
      {presentation?.summary && (
        <Text size="sm" c={DARK.muted}>
          {presentation?.summary}
        </Text>
      )}
      {nodeType === "event" && (
        <ReferenceSelect label="Event" value={value.event} names={Object.keys(record(events))}
          panel="events" onChange={(event) => onChange({ ...value, event })}
          description="Choose the payload contract shared by your flows." />
      )}
      {nodeType === "lookup" && (
        <>
          <ReferenceSelect
            label="Lookup provider"
            value={value.provider}
            names={Object.keys(record(providers))}
            panel="lookups"
            onChange={(provider) => onChange({ ...value, provider })}
            description="Providers own connection settings, timeouts and bounded caches."
          />
          <InactiveSettings
            value={value}
            fields={["table", "cache", "negative_ttl"]}
            onRemove={() =>
              changeMode(
                value,
                ["table", "cache", "negative_ttl"],
                {},
                onChange,
                props.scope,
              )
            }
          />
        </>
      )}
      {render(visible)}
      {nodeType === "http_trigger" && (
        <TriggerMethods
          value={value.methods}
          onChange={(methods) => onChange({ ...value, methods })}
        />
      )}
      {nodeType === "read_body" &&
        typeof value.max_bytes === "number" &&
        value.max_bytes > 0 && (
          <Text size="xs" c={DARK.muted}>
            Body read limit:{" "}
            {(value.max_bytes / 1048576).toLocaleString(undefined, {
              maximumFractionDigits: 3,
            })}{" "}
            MiB ({value.max_bytes.toLocaleString()} bytes).
          </Text>
        )}
      {nodeType === "lookup" && !value.name && nodeId && (
        <Text size="xs" c={DARK.muted}>
          Result variable defaults to {nodeId}.
        </Text>
      )}
      {nodeType === "code" && (
        <Stack gap="xs">
          <Text size="xs" c={DARK.muted}>
            Example: return the body and variables unchanged.
          </Text>
          <Code block>{CODE_EXAMPLE}</Code>
          <Button
            size="xs"
            variant="light"
            onClick={() => onChange({ ...value, script: CODE_EXAMPLE })}
          >
            {value.script
              ? "Replace script with example"
              : "Use example script"}
          </Button>
        </Stack>
      )}
      {secondary.length > 0 && (
        <Sections
          defaultOpen={[]}
          sections={[
            {
              value: "advanced",
              label: "Advanced settings",
              render: () => render(secondary),
            },
          ]}
        />
      )}
    </Stack>
  );
}

function TriggerMethods({
  value,
  onChange,
}: {
  value: unknown;
  onChange: (methods: string[]) => void;
}) {
  const methods = Array.isArray(value) ? value.map(String) : [];
  const standard = [
    "POST",
    "GET",
    "HEAD",
    "PUT",
    "PATCH",
    "DELETE",
    "OPTIONS",
    "CONNECT",
    "TRACE",
    "ANY",
  ];
  const [draft, setDraft] = useState("");
  const [attempted, setAttempted] = useState(false);
  const custom = draft.trim();
  const invalid = !custom || !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(custom);
  return (
    <Stack gap="xs">
      <MultiSelect
        label="Accepted HTTP methods"
        data-field-path="methods"
        data={[...new Set([...standard, ...methods])]}
        value={methods}
        searchable
        placeholder="Default: POST"
        description="Empty uses POST. ANY accepts every HTTP method. Imported custom methods are retained. GET/HEAD flows may need empty-body handling."
        onChange={onChange}
      />
      <Group align="flex-start">
        <TextInput
          style={{ flex: 1 }}
          label="Custom HTTP method"
          value={draft}
          error={
            attempted && invalid ? "Enter an HTTP method token." : undefined
          }
          onChange={(event) => {
            setDraft(event.currentTarget.value);
            setAttempted(false);
          }}
        />
        <Button
          variant="light"
          mt={24}
          onClick={() => {
            setAttempted(true);
            if (invalid) return;
            onChange([...new Set([...methods, custom.toUpperCase()])]);
            setDraft("");
            setAttempted(false);
          }}
        >
          Add method
        </Button>
      </Group>
    </Stack>
  );
}
