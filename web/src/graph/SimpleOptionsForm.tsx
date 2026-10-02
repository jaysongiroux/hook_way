import { useState } from "react";
import {
  Alert,
  Button,
  Code,
  Group,
  MultiSelect,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { Fields, type OptionsProps } from "./FormPresentation";
import { Sections } from "./InspectorSections";
import { NODE_SCHEMAS, schemaProperties, schemaRequired } from "./nodeSchema";
import { goDurationIssue } from "./goDuration";
import { DARK } from "../tokens";

export const SIMPLE_NODE_TYPES = [
  "http_trigger",
  "read_body",
  "ip_allowlist",
  "loop_guard",
  "replay_guard",
  "lookup",
  "code",
  "rate_limit",
];
export const CODE_EXAMPLE = "return { body: input.body, vars: input.vars };";
const primary: Record<string, string[]> = {
  http_trigger: ["path"],
  read_body: ["max_bytes", "allow_empty"],
  ip_allowlist: ["cidrs"],
  loop_guard: ["max_hops"],
  replay_guard: ["ttl", "key"],
  lookup: ["provider", "key", "name"],
  code: ["script"],
  rate_limit: ["rpm", "burst"],
};
const summaries: Record<string, string> = {
  http_trigger:
    "Use one endpoint per flow. Connect request checks, then Read request body, then a node that verifies or skips authentication.",
  read_body:
    "Set the maximum request size here before verifying the sender. This node enforces the limit; server.max_body_bytes is used for validation warnings and does not override it.",
  ip_allowlist:
    "Accept requests only from the listed IP addresses or networks. Place this gate before read_body. Configure trusted proxies separately so the client address is reliable.",
  loop_guard:
    "Place this before Read request body. Requests are rejected at the configured hop limit; omitted or zero uses 3.",
  replay_guard:
    "Place this after authentication. Choose how long to remember request keys; each replica has its own cache.",
  lookup:
    "Your embedding application must supply the lookup provider. Select its name and a key, then store the result in a string variable for later nodes.",
  code: "Run a short JavaScript function body with isolated input.body and input.vars. Input headers and query values are redacted; selected upstream response values may also be available. Return an object containing body and/or vars; returned variable values are converted to strings.",
  rate_limit:
    "Limit requests per key in this process. Each replica has its own limiter. Choose the request rate, optional burst allowance, and how requests share a counter.",
};
const help: Record<string, Record<string, string>> = {
  http_trigger: {
    path: "HTTP route, such as /events or /events/{record_id}. Route captures become variables. Empty method selection uses POST.",
  },
  read_body: {
    max_bytes:
      "Positive limit in bytes. 1 MiB = 1048576 bytes. Account for larger multipart wire payloads separately from decoded file limits.",
    allow_empty:
      "Permit an empty request body, for example when accepting GET or HEAD. Authentication and subsequent body parsing must also support that request.",
  },
  ip_allowlist: {
    cidrs:
      "Comma-separated IP addresses or CIDR networks, such as 192.0.2.10, 198.51.100.0/24, 2001:db8::/32. At least one entry is required.",
  },
  loop_guard: {
    max_hops:
      "Maximum forwarded hops. Omitted or 0 uses 3; requests with a hop count at or above the effective limit are rejected.",
  },
  replay_guard: {
    ttl: "How long to remember a request key, such as 5m. Omitted uses 5 minutes.",
    key: "Optional variable template, such as {header.X-Event-Id}. Omitted hashes the raw request body. Path variables and request headers are available.",
    max_entries:
      "Maximum cached keys per endpoint in this process. Omitted uses the generated default; oldest entries are evicted when full.",
  },
  lookup: {
    provider:
      "Name of a lookup provider injected by your embedding application. Typing a backend name does not configure a provider.",
    key: "Use a variable template such as {record_id}, or a body selection such as {body.record_id}.",
    name: "Variable that receives the result; omitted uses this node's ID.",
    table:
      "Provider-specific table setting. Confirm its meaning with the injected provider.",
    cache:
      "Optional cache configuration for providers that support it. This authoring block does not supply a lookup provider.",
    "cache.ttl":
      "Positive Go duration for cached successful lookups; interpreted by the injected provider.",
    "cache.max_entries":
      "Maximum successful lookup entries for providers supporting this cache configuration.",
    fallback:
      "Optional string returned when a lookup finds no result. Omitted causes the lookup to fail.",
    negative_ttl:
      "Nonnegative Go duration for caching misses when supported by the provider. 0 disables negative caching.",
  },
  code: {
    script:
      "JavaScript function body with top-level return. No require, fetch, timers or filesystem access. For XML input, preserve the tagged XML node-tree shape.",
    timeout:
      "Positive Go duration, such as 1s; omitted uses the generated default.",
    max_input_bytes:
      "Maximum JSON-encoded script input in bytes, including body and metadata.",
    max_output_bytes: "Maximum JSON-encoded script result in bytes.",
    max_call_stack:
      "Maximum JavaScript call-stack depth; omitted uses the generated default.",
  },
  rate_limit: {
    rpm: "Positive requests per minute for each key in this process.",
    burst:
      "Maximum burst allowance. Omitted or 0 uses the configured requests per minute.",
    key_by:
      "Use source_ip or header.Name. Empty or unrecognized values fall back to source IP.",
  },
};

export function SimpleOptionsForm(props: OptionsProps) {
  const { nodeType, value, onChange, nodeId } = props;
  const meta = NODE_SCHEMAS[nodeType];
  if (!meta)
    return (
      <Alert color="orange">
        This node type is not in the runtime catalog. Imported options are
        retained; choose a supported node type before deploying.
      </Alert>
    );
  const keys = Object.keys(schemaProperties(meta.schema));
  const visible = primary[nodeType] ?? keys;
  const managed =
    nodeType === "http_trigger"
      ? ["methods"]
      : nodeType === "rate_limit"
        ? ["key_by"]
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
        provider: "Custom provider name",
        name: "Result variable",
        script: "JavaScript function body",
        rpm: "Requests per minute",
        burst: "Burst allowance",
      }}
      fieldHelp={(path) =>
        help[nodeType]?.[path] ??
        help[nodeType]?.[path.split(".").at(-1) ?? path]
      }
      groupObjects
    />
  );
  return (
    <Stack gap="sm">
      {summaries[nodeType] && (
        <Text size="sm" c={DARK.muted}>
          {summaries[nodeType]}
        </Text>
      )}
      {nodeType === "lookup" && (
        <Alert color="orange" title="Requires a custom embedding application">
          An injected provider is required. Configure typed routing through the
          Routing workspace and route node when using the standalone server.
        </Alert>
      )}
      {render(visible)}
      {nodeType === "http_trigger" && (
        <TriggerMethods
          value={value.methods}
          onChange={(methods) => onChange({ ...value, methods })}
        />
      )}
      {nodeType === "rate_limit" && <RateLimitKey {...props} />}
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

function RateLimitKey({ value, onChange }: OptionsProps) {
  const key = String(value.key_by ?? "source_ip");
  const mode =
    key === "" || key === "source_ip"
      ? "source_ip"
      : key.startsWith("header.")
        ? "header"
        : "imported";
  const header = mode === "header" ? key.slice(7) : "";
  return (
    <Stack gap="xs">
      <Select
        label="Rate limit key"
        data-field-path="key_by"
        value={mode}
        allowDeselect={false}
        data={[
          { value: "source_ip", label: "Source IP (default)" },
          { value: "header", label: "Request header" },
          ...(mode === "imported"
            ? [{ value: "imported", label: "Imported unrecognized key" }]
            : []),
        ]}
        description="Requests sharing this value share a counter in this process. Missing header values fall back to source IP."
        onChange={(next) => {
          if (next && next !== "imported")
            onChange({
              ...value,
              key_by: next === "header" ? "header." : "source_ip",
            });
        }}
      />
      {mode === "header" && (
        <TextInput
          label="Key header name"
          aria-label="Key header name"
          data-field-path="key_by"
          required
          value={header}
          error={
            !header
              ? "Enter the request header name."
              : !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(header)
                ? "Use an HTTP header name."
                : undefined
          }
          description="Header name only, such as X-Client-Id. Exports as header.X-Client-Id."
          onChange={(event) =>
            onChange({
              ...value,
              key_by: `header.${event.currentTarget.value}`,
            })
          }
        />
      )}
      {mode === "imported" && (
        <Alert color="orange">
          Imported key {key} is retained. The runtime falls back to source IP
          for unrecognized keys. Select a supported key to change this setting.
        </Alert>
      )}
    </Stack>
  );
}
