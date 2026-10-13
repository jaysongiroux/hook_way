import {
  Alert,
  Modal,
  NativeSelect,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { ReferenceSelect, type OptionsProps } from "./FormPresentation";
import { SchemaForm } from "./SchemaForm";
import { isSchemaObject, type JSONSchema } from "./nodeSchema";
import {
  record,
  credentialNameError,
  WorkspaceEntries,
} from "./WorkspaceEntries";
import { clearDraftBranch } from "./formDrafts";
import { useGraphStore } from "./store";
import catalog from "./workspaceSchema.json";

const identifierError = (name: string) =>
  /^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(name)
    ? undefined
    : "Use letters, digits and underscores; start with a letter or underscore (up to 64 characters).";
const flagTypes = ["boolean", "string", "number", "json"];

export function LaunchDarklyPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const value = useGraphStore((s) => s.configExtra.launchdarkly);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  const map = (catalog as Record<string, JSONSchema>).launchdarkly;
  const schema = isSchemaObject(map)
    ? (map.additionalProperties as JSONSchema)
    : false;
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="LaunchDarkly connections"
      size="xl"
      closeButtonProps={{ "aria-label": "Close LaunchDarkly connections" }}
    >
      <Stack gap="md">
        <Text size="sm">
          Each connection selects a LaunchDarkly environment. The server reads
          its SDK key from an environment variable and shares one client across
          flows. The builder never contacts LaunchDarkly.
        </Text>
        <WorkspaceEntries
          label="LaunchDarkly connection"
          value={value}
          onChange={(launchdarkly) => setConfigExtra({ launchdarkly })}
          initial={() => ({ sdk_key_env: "LAUNCHDARKLY_SDK_KEY" })}
          nameError={credentialNameError}
        >
          {(name, entry, change) => (
            <SchemaForm
              schema={schema}
              value={entry}
              onChange={(v) => change(record(v))}
              scope={`launchdarkly/${name}`}
              labels={{
                sdk_key_env: "SDK key environment variable",
                initialization_timeout: "Initialization timeout",
                send_events: "Send evaluation events",
              }}
              fieldHelp={(path) =>
                ({
                  sdk_key_env:
                    "Enter the environment variable name, such as LAUNCHDARKLY_SDK_KEY. Never paste the SDK key.",
                  initialization_timeout:
                    "Wait up to 5s by default (maximum 1m). A network timeout permits serving with fallbacks while the client reconnects.",
                  send_events:
                    "Enabled by default. Custom attributes are private; context key and kind still reach LaunchDarkly. Turn off to disable SDK analytics events.",
                })[path]
              }
            />
          )}
        </WorkspaceEntries>
      </Stack>
    </Modal>
  );
}

function Selector({
  value,
  onChange,
  scope,
  label,
  keyOnly = false,
}: {
  value: unknown;
  onChange: (v: Record<string, unknown>) => void;
  scope: string;
  label: string;
  keyOnly?: boolean;
}) {
  const selector = record(value);
  const mode = Object.prototype.hasOwnProperty.call(selector, "value")
    ? "literal"
    : String(selector.source ?? "body");
  return (
    <Stack gap="xs">
      <NativeSelect
        label={`${label} source`}
        value={mode}
        data={[
          { value: "body", label: "Body field" },
          { value: "header", label: "Request header" },
          { value: "vars", label: "Variable" },
          { value: "literal", label: "Literal value" },
        ]}
        onChange={(e) => {
          clearDraftBranch(scope, "");
          const source = e.currentTarget.value;
          onChange(
            source === "literal"
              ? { value: keyOnly ? "" : null }
              : { source, path: "" },
          );
        }}
      />
      {mode === "literal" ? (
        <SchemaForm
          schema={{
            type: "object",
            properties: { value: keyOnly ? { type: "string" } : {} },
            required: ["value"],
          }}
          value={selector}
          onChange={(v) => onChange(record(v))}
          labels={{ value: `${label} value` }}
          scope={scope}
        />
      ) : (
        <TextInput
          label={`${label} path`}
          aria-label={`${label} path`}
          value={String(selector.path ?? "")}
          required
          description={
            mode === "body"
              ? "Dotted object path, such as tenant.id. Array indexes are not supported."
              : mode === "header"
                ? "Request header name; case-insensitive."
                : "Existing variable name."
          }
          onChange={(e) =>
            onChange({ ...selector, source: mode, path: e.currentTarget.value })
          }
        />
      )}
      {Object.prototype.hasOwnProperty.call(selector, "value") &&
        (selector.source !== undefined || selector.path !== undefined) && (
          <Alert color="orange">
            Imported selector contains both a literal and a source. Select its
            source again to repair it.
          </Alert>
        )}
    </Stack>
  );
}

export function LaunchDarklyForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const launchdarkly = useGraphStore((s) => s.configExtra.launchdarkly);
  const connections = Object.keys(record(launchdarkly));
  const context = record(value.context);
  const set = (key: string, v: unknown) => onChange({ ...value, [key]: v });
  const setContext = (key: string, v: unknown) =>
    set("context", { ...context, [key]: v });
  return (
    <Stack gap="md">
      <Text size="sm">
        Evaluate only the flags listed below. Results stay outside the webhook
        body. Missing flags, invalid event contexts and SDK failures use each
        flag’s explicit fallback.
      </Text>
      <ReferenceSelect
        label="LaunchDarkly connection"
        value={value.connection}
        names={connections}
        onChange={(v) => set("connection", v)}
        panel="launchdarkly"
      />
      <TextInput
        label="Result namespace"
        aria-label="Result namespace"
        required
        value={String(value.name ?? "")}
        error={identifierError(String(value.name ?? ""))}
        description="Unique within this flow. Conditions read namespace.alias.value or namespace.alias.used_fallback."
        onChange={(e) => set("name", e.currentTarget.value)}
      />
      <TextInput
        label="Context kind"
        value={String(context.kind ?? "")}
        placeholder="Default: user"
        description="Use user for user targeting or your organization context kind for tenant targeting."
        onChange={(e) => setContext("kind", e.currentTarget.value)}
      />
      <Selector
        label="Context key"
        keyOnly
        value={context.key}
        scope={`${scope}/context/key`}
        onChange={(v) => setContext("key", v)}
      />
      <Text size="sm" fw={600}>
        Context attributes
      </Text>
      <WorkspaceEntries
        label="context attribute"
        value={context.attributes}
        initial={() => ({ source: "body", path: "" })}
        nameError={(name) =>
          name && !["key", "kind", "_meta"].includes(name)
            ? undefined
            : "Choose a nonreserved attribute name."
        }
        onChange={(v) => setContext("attributes", v)}
      >
        {(name, entry, change) => (
          <Selector
            label={`Attribute ${name}`}
            value={entry}
            onChange={change}
            scope={`${scope}/context/attributes/${name}`}
          />
        )}
      </WorkspaceEntries>
      <Text size="sm" fw={600}>
        Named flags
      </Text>
      <WorkspaceEntries
        label="flag alias"
        value={value.flags}
        initial={() => ({ key: "", type: "boolean", fallback: false })}
        nameError={identifierError}
        onChange={(v) => set("flags", v)}
      >
        {(alias, entry, change) => {
          const type = String(entry.type ?? "boolean");
          const fallbackSchema: JSONSchema = type === "json" ? {} : { type };
          return (
            <Stack gap="sm">
              <TextInput
                label="LaunchDarkly flag key"
                aria-label="LaunchDarkly flag key"
                required
                value={String(entry.key ?? "")}
                description="Exact flag key in LaunchDarkly. Dots are allowed here; conditions use the alias instead."
                onChange={(e) =>
                  change({ ...entry, key: e.currentTarget.value })
                }
              />
              <NativeSelect
                label="Flag type"
                value={type}
                data={flagTypes}
                description="Changing the type resets its fallback."
                onChange={(e) => {
                  clearDraftBranch(`${scope}/flags/${alias}`, "");
                  const type = e.currentTarget.value;
                  change({
                    ...entry,
                    type,
                    fallback:
                      type === "boolean"
                        ? false
                        : type === "number"
                          ? 0
                          : type === "json"
                            ? null
                            : "",
                  });
                }}
              />
              <SchemaForm
                schema={{
                  type: "object",
                  properties: { fallback: fallbackSchema },
                  required: ["fallback"],
                }}
                value={entry}
                onChange={(v) => change(record(v))}
                labels={{ fallback: "Fallback value" }}
                errors={
                  type === "string" && entry.fallback === ""
                    ? { fallback: "" }
                    : undefined
                }
                fieldHelp={() =>
                  "Required. Used when evaluation fails; JSON null is a valid JSON fallback."
                }
                scope={`${scope}/flags/${alias}`}
              />
              {!Object.prototype.hasOwnProperty.call(entry, "fallback") && (
                <Alert color="red">
                  Supply an explicit fallback, including false, an empty string,
                  zero or JSON null.
                </Alert>
              )}
              <TextInput
                label="Variable binding"
                value={String(entry.variable ?? "")}
                description="Optional. Strings stay plain; other values become compact JSON text for existing templates."
                onChange={(e) => {
                  const next = { ...entry };
                  if (e.currentTarget.value)
                    next.variable = e.currentTarget.value;
                  else delete next.variable;
                  change(next);
                }}
              />
              <Text size="xs" c="dimmed">
                Condition path: {String(value.name || "namespace")}.{alias}
                .value
              </Text>
            </Stack>
          );
        }}
      </WorkspaceEntries>
    </Stack>
  );
}
