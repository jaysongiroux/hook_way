import { useEffect, useMemo, useState } from "react";
import {
  Accordion,
  ActionIcon,
  Alert,
  Button,
  Checkbox,
  Divider,
  Group,
  Modal,
  PasswordInput,
  Skeleton,
  Stack,
  Tabs,
  Text,
  TextInput,
} from "@mantine/core";
import { TbAlertTriangle, TbHistory, TbPlus, TbTrash } from "react-icons/tb";
import { DARK } from "../tokens";
import { type JSONSchema, schemaProperties } from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";
import { loadReplaySchema } from "./validate";

const SCOPES = ["capture", "read", "payload", "retry", "metrics"];

const CORE_REPLAY_SCHEMA: JSONSchema = {
  type: "object",
  properties: {
    url: { type: "string" },
    capture_token: { type: "string" },
    max_record_bytes: { type: "integer", minimum: 1 },
    tls: {
      type: "object",
      properties: {
        ca_cert: { type: "string" },
        client_cert: { type: "string" },
        client_key: { type: "string" },
        server_name: { type: "string" },
        insecure_skip_verify: { type: "boolean" },
      },
    },
    control: {
      type: "object",
      properties: {
        addr: { type: "string" },
        token: { type: "string" },
        max_concurrent: { type: "integer", minimum: 1 },
      },
    },
  },
};

const SECTION_COPY: Record<string, { label: string; description: string }> = {
  server: {
    label: "Server",
    description: "Listener for the replay API, health checks, and readiness.",
  },
  logging: {
    label: "Logging",
    description: "Structured replay-service log level.",
  },
  metrics: {
    label: "Metrics",
    description: "Prometheus endpoint and optional dedicated listener.",
  },
  database: {
    label: "Database",
    description: "Shared PostgreSQL connection and client TLS.",
  },
  core: {
    label: "Core callback",
    description: "Private control endpoint used when a retry runs.",
  },
  retention: {
    label: "Retention",
    description: "Lifetime, capacity, artifact size, and cleanup interval.",
  },
  workers: {
    label: "Workers",
    description: "Per-replica retry concurrency, deadline, and lease.",
  },
  keys: {
    label: "API keys",
    description: "Named bearer tokens with least-privilege scopes.",
  },
};

const HELP: Record<string, string> = {
  url: "Replay service URL used by core to capture failed deliveries.",
  capture_token: "Key with capture scope. Prefer ${REPLAY_CAPTURE_TOKEN}.",
  max_record_bytes:
    "Largest artifact core will send with bytes rather than metadata only.",
  addr: "Listener address such as :8080.",
  token: "Use an environment reference rather than a literal secret.",
  max_concurrent: "Maximum replay deliveries core accepts at once.",
  ca_cert: "CA certificate path used to verify the remote service.",
  client_cert: "Client certificate path; configure with client key.",
  client_key: "Client private-key path; configure with client certificate.",
  server_name: "TLS server-name override.",
  insecure_skip_verify:
    "Disables certificate verification. Do not use in production.",
  level: "debug, info, warn, or error.",
  enabled: "Expose Prometheus metrics.",
  path: "HTTP path for metrics, normally /metrics.",
  runtime_metrics: "Include Go runtime and process collectors.",
  max_conns: "Maximum PostgreSQL pool size for each replay replica.",
  control_url: "Absolute URL of core's private replay control listener.",
  control_token: "Must match core replay.control.token.",
  ttl: "How long a failed-delivery record remains available.",
  max_records: "Maximum active records retained in PostgreSQL.",
  max_bytes: "Maximum combined retained artifact bytes.",
  gc_interval: "Cleanup period; 0 disables the in-process sweep.",
  concurrency: "Retries executed concurrently by each replay replica.",
  execution_timeout: "Deadline for one retry run.",
  lease: "Must be at least execution timeout plus 15 seconds.",
};

export function ReplaySettingsPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const coreReplayConfig = useGraphStore((s) => s.coreReplayConfig);
  const replayServiceConfig = useGraphStore((s) => s.replayServiceConfig);
  const setCoreReplayConfig = useGraphStore((s) => s.setCoreReplayConfig);
  const setReplayServiceConfig = useGraphStore((s) => s.setReplayServiceConfig);
  const addReplayConfiguration = useGraphStore((s) => s.addReplayConfiguration);
  const [schema, setSchema] = useState<JSONSchema>();
  const [schemaFailed, setSchemaFailed] = useState(false);

  useEffect(() => {
    if (!opened || schema || schemaFailed) return;
    void loadReplaySchema().then((loaded) => {
      if (loaded) setSchema(loaded as JSONSchema);
      else setSchemaFailed(true);
    });
  }, [opened, schema, schemaFailed]);

  const advisories = useMemo(
    () => replayAdvisories(coreReplayConfig, replayServiceConfig),
    [coreReplayConfig, replayServiceConfig],
  );
  const configured = coreReplayConfig !== null || replayServiceConfig !== null;

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Replay settings"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Replay settings" }}
    >
      {!configured ? (
        <Stack align="flex-start" gap="md" py="lg">
          <TbHistory aria-hidden size={28} color={DARK.muted} />
          <Stack gap={4} maw={600}>
            <Text fw={600}>Add the optional replay service</Text>
            <Text size="sm" c="dimmed">
              This creates the core connection block and a separate replay.yaml
              using the repository&apos;s development defaults. Nothing is sent
              to a running service.
            </Text>
          </Stack>
          <Button
            leftSection={<TbPlus aria-hidden size={16} />}
            onClick={addReplayConfiguration}
          >
            Add replay configuration
          </Button>
        </Stack>
      ) : (
        <Stack gap="sm">
          {(coreReplayConfig === null || replayServiceConfig === null) && (
            <Alert
              color="yellow"
              icon={<TbAlertTriangle aria-hidden />}
              title="Replay setup is incomplete"
            >
              Add the missing document to configure both sides of the
              connection.
              <Button
                size="compact-sm"
                variant="subtle"
                ml="xs"
                onClick={addReplayConfiguration}
              >
                Add missing config
              </Button>
            </Alert>
          )}
          {advisories.map((message) => (
            <Alert
              key={message}
              color="yellow"
              icon={<TbAlertTriangle aria-hidden />}
              title="Check shared credentials"
            >
              {message}
            </Alert>
          ))}
          <Tabs
            defaultValue={coreReplayConfig !== null ? "core" : "service"}
            keepMounted={false}
          >
            <Tabs.List>
              <Tabs.Tab value="core">Core integration</Tabs.Tab>
              <Tabs.Tab value="service">Replay service</Tabs.Tab>
            </Tabs.List>
            <Tabs.Panel value="core" pt="md">
              {coreReplayConfig === null ? (
                <MissingDocument
                  name="core replay block"
                  onAdd={addReplayConfiguration}
                />
              ) : (
                <Stack gap="sm">
                  <Text size="sm" c="dimmed" maw={720}>
                    Settings written to core.yaml under{" "}
                    <Text span ff="monospace">
                      replay:
                    </Text>
                    . Core uses these to capture failures and accept private
                    retry callbacks.
                  </Text>
                  <SchemaForm
                    scope="core/replay"
                    schema={CORE_REPLAY_SCHEMA}
                    value={coreReplayConfig}
                    onChange={(value) =>
                      setCoreReplayConfig(value as Record<string, unknown>)
                    }
                    fieldHelp={(name) => HELP[name]}
                    groupObjects
                    secretFields={["capture_token", "token"]}
                  />
                </Stack>
              )}
            </Tabs.Panel>
            <Tabs.Panel value="service" pt="md">
              {replayServiceConfig === null ? (
                <MissingDocument
                  name="replay.yaml"
                  onAdd={addReplayConfiguration}
                />
              ) : schemaFailed ? (
                <Alert color="red" title="Replay schema unavailable">
                  Reload the builder to restore structured replay settings.
                  Imported YAML is still preserved.
                </Alert>
              ) : !schema ? (
                <Stack gap="xs" aria-label="Loading replay settings">
                  <Skeleton height={18} width="45%" />
                  <Skeleton height={44} />
                  <Skeleton height={44} />
                </Stack>
              ) : (
                <ReplayServiceEditor
                  schema={schema}
                  config={replayServiceConfig}
                  onChange={setReplayServiceConfig}
                />
              )}
            </Tabs.Panel>
          </Tabs>
        </Stack>
      )}
    </Modal>
  );
}

function MissingDocument({ name, onAdd }: { name: string; onAdd: () => void }) {
  return (
    <Stack align="flex-start" gap="sm" py="md">
      <Text size="sm" c="dimmed">
        No {name} is present in this workspace.
      </Text>
      <Button size="xs" variant="light" onClick={onAdd}>
        Add missing config
      </Button>
    </Stack>
  );
}

function ReplayServiceEditor({
  schema,
  config,
  onChange,
}: {
  schema: JSONSchema;
  config: Record<string, unknown>;
  onChange: (config: Record<string, unknown>) => void;
}) {
  const properties = schemaProperties(schema);
  const update = (key: string, value: unknown) =>
    onChange({ ...config, [key]: value });
  return (
    <Stack gap="sm">
      <Text size="sm" c="dimmed" maw={720}>
        Settings written to the separate replay.yaml used by hookway-replay.
        Defaults stay omitted unless you change or seed them.
      </Text>
      <Accordion defaultValue="server">
        {[
          "server",
          "logging",
          "metrics",
          "database",
          "core",
          "retention",
          "workers",
        ].map((key) => (
          <Accordion.Item key={key} value={key}>
            <Accordion.Control>
              <Group justify="space-between" wrap="nowrap" pr="xs">
                <Text size="sm" fw={600}>
                  {SECTION_COPY[key].label}
                </Text>
                <Text size="xs" c="dimmed" visibleFrom="sm">
                  {SECTION_COPY[key].description}
                </Text>
              </Group>
            </Accordion.Control>
            <Accordion.Panel>
              <Stack gap="xs">
                <Text size="xs" c="dimmed" hiddenFrom="sm">
                  {SECTION_COPY[key].description}
                </Text>
                <SchemaForm
                  scope={`replay/${key}`}
                  schema={properties[key]}
                  value={asRecord(config[key])}
                  onChange={(value) => update(key, value)}
                  fieldHelp={(name) => HELP[name]}
                  groupObjects
                  secretFields={key === "core" ? ["control_token"] : undefined}
                />
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        ))}
        <Accordion.Item value="keys">
          <Accordion.Control>
            <Group justify="space-between" wrap="nowrap" pr="xs">
              <Text size="sm" fw={600}>
                API keys
              </Text>
              <Text size="xs" c="dimmed" visibleFrom="sm">
                Named bearer tokens with least-privilege scopes.
              </Text>
            </Group>
          </Accordion.Control>
          <Accordion.Panel>
            <ReplayKeysEditor
              value={Array.isArray(config.keys) ? config.keys : []}
              onChange={(value) => update("keys", value)}
            />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  );
}

function ReplayKeysEditor({
  value,
  onChange,
}: {
  value: unknown[];
  onChange: (keys: unknown[]) => void;
}) {
  const rows = value.map(asRecord);
  const update = (index: number, patch: Record<string, unknown>) =>
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  const add = () => {
    const names = new Set(rows.map((row) => String(row.name ?? "")));
    let name = "key";
    for (let i = 2; names.has(name); i += 1) name = `key_${i}`;
    onChange([...rows, { name, token: "${REPLAY_TOKEN}", scopes: ["read"] }]);
  };
  return (
    <Stack gap="md">
      <Text size="xs" c="dimmed">
        Use separate capture, operator, and metrics credentials. Literal tokens
        must be at least 32 characters.
      </Text>
      {rows.map((row, index) => {
        const unknown = (
          Array.isArray(row.scopes) ? row.scopes.map(String) : []
        ).filter((scope) => !SCOPES.includes(scope));
        const scopes = [...SCOPES, ...unknown];
        return (
          <Stack
            key={`${String(row.name ?? "key")}-${index}`}
            gap="xs"
            data-testid={`replay-key-${index}`}
          >
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Text fw={600} size="sm" ff="monospace">
                {String(row.name || `key_${index + 1}`)}
              </Text>
              <ActionIcon
                aria-label={`Remove replay key ${index + 1}`}
                variant="subtle"
                color="red"
                onClick={() => onChange(rows.filter((_, i) => i !== index))}
              >
                <TbTrash aria-hidden size={16} />
              </ActionIcon>
            </Group>
            <Group grow align="flex-start">
              <TextInput
                label="Name"
                value={String(row.name ?? "")}
                onChange={(event) =>
                  update(index, { name: event.currentTarget.value })
                }
              />
              <PasswordInput
                label="Token"
                value={String(row.token ?? "")}
                onChange={(event) =>
                  update(index, { token: event.currentTarget.value })
                }
              />
            </Group>
            <Checkbox.Group
              label="Scopes"
              value={Array.isArray(row.scopes) ? row.scopes.map(String) : []}
              onChange={(selected) => update(index, { scopes: selected })}
            >
              <Group mt={6} gap="md">
                {scopes.map((scope) => (
                  <Checkbox key={scope} value={scope} label={scope} />
                ))}
              </Group>
            </Checkbox.Group>
            {index < rows.length - 1 && <Divider />}
          </Stack>
        );
      })}
      <Button
        size="xs"
        variant="light"
        leftSection={<TbPlus aria-hidden size={14} />}
        onClick={add}
        style={{ alignSelf: "flex-start" }}
      >
        Add API key
      </Button>
    </Stack>
  );
}

function replayAdvisories(
  coreConfig: Record<string, unknown> | null,
  replayConfig: Record<string, unknown> | null,
): string[] {
  if (!coreConfig || !replayConfig) return [];
  const out: string[] = [];
  const captureToken = String(coreConfig.capture_token ?? "");
  const keys = Array.isArray(replayConfig.keys)
    ? replayConfig.keys.map(asRecord)
    : [];
  if (
    captureToken &&
    !keys.some(
      (key) =>
        Array.isArray(key.scopes) &&
        key.scopes.map(String).includes("capture") &&
        key.token === captureToken,
    )
  ) {
    out.push(
      "Core's capture token does not match a capture-scoped replay API key.",
    );
  }
  const coreControlToken = String(asRecord(coreConfig.control).token ?? "");
  const replayControlToken = String(
    asRecord(replayConfig.core).control_token ?? "",
  );
  if (
    coreControlToken &&
    replayControlToken &&
    coreControlToken !== replayControlToken
  ) {
    out.push(
      "Core's control token and replay.yaml's core control token do not match.",
    );
  }
  return out;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
