import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Code,
  Group,
  PasswordInput,
  Select,
  Stack,
  Tabs,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type { Client, Tool } from "@modelcontextprotocol/client";
import { Fields, object, type OptionsProps } from "./FormPresentation";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";
import { HTTPOptionsForm } from "./ProxyOptionsForm";
import { Sections } from "./InspectorSections";
import { clearDraftBranch, nodeScope, useFieldDraft } from "./formDrafts";

function newMCPTool(name = "new_tool"): Record<string, unknown> {
  return {
    name,
    description: "Describe what this tool does.",
    read_only: true,
    input_schema: { type: "object", additionalProperties: false },
    output_schema: { type: "object" },
    access: { api_keys: ["reader"] },
  };
}

function MCPToolName({
  name,
  otherNames,
  scope,
  onChange,
}: {
  name: string;
  otherNames: unknown[];
  scope: string;
  onChange: (name: string) => void;
}) {
  const [draft, save] = useFieldDraft(scope, "tools.name");
  return (
    <Stack gap={4}>
      <TextInput
        label="Tool name"
        aria-label="Tool name"
        required
        value={draft?.text ?? name}
        error={draft?.error || undefined}
        onChange={(event) => {
          const next = event.currentTarget.value;
          const error =
            !/^[a-zA-Z0-9_-]{1,64}$/.test(next) ||
            ["out", "on_error"].includes(next)
              ? "Use 1–64 letters, numbers, underscores or hyphens. The names out and on_error are reserved."
              : otherNames.includes(next)
                ? "Another tool already uses this name. Choose a unique name."
                : "";
          if (error) save({ text: next, error });
          else {
            save(undefined);
            onChange(next);
          }
        }}
      />
      {draft?.error && (
        <Button
          size="compact-xs"
          variant="subtle"
          onClick={() => save(undefined)}
        >
          Revert tool name
        </Button>
      )}
    </Stack>
  );
}

export function MCPOptionsForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const tools = Array.isArray(value.tools) ? value.tools.map(object) : [];
  const [savedRows, saveRows] = useFieldDraft(scope, "tools._rows");
  const rowIDs = useRef<string[]>(savedRows?.rowIds ?? []);
  while (rowIDs.current.length < tools.length)
    rowIDs.current.push(crypto.randomUUID());
  useEffect(() => {
    if (JSON.stringify(savedRows?.rowIds) !== JSON.stringify(rowIDs.current))
      saveRows({ rowIds: [...rowIDs.current], error: "" });
  }, [tools.length, savedRows?.rowIds, saveRows]);
  const auth = object(value.auth);
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const triggerID =
    nodes.find((n) => n.id === props.nodeId || n.data.yamlId === props.nodeId)
      ?.id ?? props.nodeId;
  const schema = schemaProperties(NODE_SCHEMAS.mcp_trigger.schema);
  const toolSchema =
    typeof schema.tools === "object" ? schema.tools.items : undefined;
  const authMode = auth.no_auth
    ? "none"
    : auth.oauth
      ? auth.api_keys
        ? "both"
        : "oauth"
      : "key";
  const changeTool = (index: number, patch: Record<string, unknown>) => {
    const previous = tools[index].name;
    if (
      typeof patch.name === "string" &&
      patch.name !== previous &&
      props.nodeId &&
      !tools.some((t, i) => i !== index && t.name === patch.name)
    ) {
      useGraphStore.setState((s) => ({
        edges: s.edges.map((e) =>
          e.source === triggerID && e.sourceHandle === previous
            ? { ...e, sourceHandle: String(patch.name) }
            : e,
        ),
      }));
    }
    onChange({
      ...value,
      tools: tools.map((t, i) => (i === index ? { ...t, ...patch } : t)),
    });
  };
  const removeTool = (index: number) => {
    clearDraftBranch(`${scope}/${rowIDs.current[index]}`);
    rowIDs.current.splice(index, 1);
    saveRows({ rowIds: [...rowIDs.current], error: "" });
    useGraphStore
      .getState()
      .onEdgesChange(
        edges
          .filter(
            (e) =>
              e.source === triggerID && e.sourceHandle === tools[index].name,
          )
          .map((e) => ({ type: "remove", id: e.id })),
      );
    onChange({ ...value, tools: tools.filter((_, i) => i !== index) });
  };
  return (
    <Tabs defaultValue="server">
      <Tabs.List grow>
        <Tabs.Tab value="server">Endpoint</Tabs.Tab>
        <Tabs.Tab value="tools">Tools ({tools.length})</Tabs.Tab>
        <Tabs.Tab value="test">Test & connect</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="server" pt="md">
        <Stack gap="md">
          <Text size="sm">
            This endpoint advertises these tools. Choose its path and public URL
            below. Add another MCP trigger in a separate flow for another
            endpoint, with its own authentication and tool catalogue.
          </Text>
          <Fields
            {...props}
            fields={["path", "public_url", "allowed_origins", "max_inflight"]}
            required={["path", "public_url"]}
          />
          <Select
            label="Client authentication"
            value={authMode}
            allowDeselect={false}
            data={[
              {
                value: "key",
                label: "API keys (recommended for a simple setup)",
              },
              { value: "oauth", label: "OAuth identity provider" },
              { value: "both", label: "API keys and OAuth" },
              { value: "none", label: "No authentication — public tools" },
            ]}
            onChange={(mode) => {
              if (!mode) return;
              onChange({
                ...value,
                auth:
                  mode === "none"
                    ? { no_auth: true }
                    : {
                        ...(mode === "key" || mode === "both"
                          ? {
                              api_keys: auth.api_keys ?? {
                                reader: {
                                  secret_ref: {
                                    provider: "env",
                                    name: "MCP_READER_TOKEN",
                                  },
                                },
                              },
                            }
                          : {}),
                        ...(mode === "oauth" || mode === "both"
                          ? {
                              oauth: auth.oauth ?? { issuer: "", audience: "" },
                            }
                          : {}),
                      },
              });
            }}
          />
          {auth.no_auth === true ? (
            <Alert color="orange" title="Public endpoint">
              Anyone who can reach this URL can invoke tools granted anonymous
              access. Configure each tool’s policy explicitly.
            </Alert>
          ) : (
            <>
              <Text size="xs" c="dimmed">
                These settings contain secret references only. API keys identify
                their named caller; shared keys grant shared access. OAuth uses
                your provider’s dedicated Hookway audience.
              </Text>
              <SchemaForm
                schema={schema.auth}
                value={auth}
                onChange={(next) => onChange({ ...value, auth: object(next) })}
                hideFields={["no_auth"]}
                scope={scope}
                path="auth"
                groupObjects
              />
            </>
          )}
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="tools" pt="md">
        <Stack gap="lg">
          <Text size="sm">
            Add a tool, then connect its named output to an HTTP request or
            native nodes. Finish each branch with MCP result.
          </Text>
          {tools.map((tool, index) => {
            const name = String(tool.name ?? "");
            const branch = edges.find(
              (e) => e.source === triggerID && e.sourceHandle === name,
            );
            const target = nodes.find((n) => n.id === branch?.target);
            const toolScope = `${scope}/${rowIDs.current[index]}`;
            return (
              <Stack key={rowIDs.current[index]} gap="sm">
                <Group justify="space-between">
                  <Text fw={600}>{name || "Unnamed tool"}</Text>
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="red"
                    onClick={() => removeTool(index)}
                  >
                    Remove tool {index + 1}
                  </Button>
                </Group>
                <Text size="xs" c="dimmed">
                  {target
                    ? `Connected to ${target.data.yamlId} (${target.data.nodeType}). Configure its REST operation or native steps on the canvas.`
                    : "This tool needs a connected branch."}
                </Text>
                {target?.data.nodeType === "http_request" && (
                  <Sections
                    defaultOpen={[]}
                    sections={[
                      {
                        value: "operation",
                        label: "Connected REST operation",
                        badge: String(target.data.options.method ?? "POST"),
                        render: () => (
                          <HTTPOptionsForm
                            nodeType="http_request"
                            nodeId={target.data.yamlId}
                            value={target.data.options}
                            scope={nodeScope(activeFlow, target.id)}
                            onChange={(options) =>
                              useGraphStore
                                .getState()
                                .updateNodeData(target.id, { options })
                            }
                          />
                        ),
                      },
                    ]}
                  />
                )}
                <MCPToolName
                  name={name}
                  otherNames={tools
                    .filter((_, i) => i !== index)
                    .map((tool) => tool.name)}
                  scope={toolScope}
                  onChange={(name) => changeTool(index, { name })}
                />
                <SchemaForm
                  schema={toolSchema}
                  value={tool}
                  onChange={(next) => changeTool(index, object(next))}
                  hideFields={[
                    "name",
                    "input_schema",
                    "output_schema",
                    "request",
                  ]}
                  scope={toolScope}
                  path="tools"
                  groupObjects
                  labels={{
                    name: "Tool name",
                    description: "Tool description",
                    read_only: "Read-only operation",
                  }}
                />
                {(["input_schema", "output_schema", "request"] as const).map(
                  (field) => (
                    <SchemaForm
                      key={field}
                      schema={{ type: "object" }}
                      value={tool[field] ?? {}}
                      onChange={(next) => changeTool(index, { [field]: next })}
                      scope={toolScope}
                      path={`tools.${field}`}
                      labels={{
                        [`tools.${field}`]:
                          field === "input_schema"
                            ? "Input JSON schema"
                            : field === "output_schema"
                              ? "Output JSON schema"
                              : "Argument mapping (variables, query, headers, body)",
                      }}
                    />
                  ),
                )}
                {tool.read_only !== true && (
                  <Alert color="orange">
                    This tool can change upstream data. Grant mutation access
                    explicitly. Automatic retries are disabled.
                  </Alert>
                )}
              </Stack>
            );
          })}
          <Button
            variant="light"
            onClick={() => {
              let suffix = tools.length + 1;
              while (tools.some((tool) => tool.name === `tool_${suffix}`))
                suffix++;
              onChange({
                ...value,
                tools: [...tools, newMCPTool(`tool_${suffix}`)],
              });
            }}
          >
            Add tool
          </Button>
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="test" pt="md">
        <MCPTester
          key={JSON.stringify([value.public_url, auth.no_auth === true])}
          endpoint={String(value.public_url ?? "")}
          anonymous={auth.no_auth === true}
        />
      </Tabs.Panel>
    </Tabs>
  );
}

function MCPTester({
  endpoint,
  anonymous,
}: {
  endpoint: string;
  anonymous: boolean;
}) {
  const [url, setURL] = useState(endpoint);
  const [token, setToken] = useState("");
  const [tools, setTools] = useState<Tool[]>([]);
  const [toolName, setToolName] = useState<string | null>(null);
  const [argsText, setArgsText] = useState("{}");
  let args: Record<string, unknown> = {};
  let argsError = "";
  try {
    const parsed: unknown = JSON.parse(argsText);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
      throw new Error();
    args = parsed as Record<string, unknown>;
  } catch {
    argsError = "Enter a valid JSON object before calling the tool.";
  }
  const [approved, setApproved] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [result, setResult] = useState("");
  const client = useRef<Client | null>(null);
  const generation = useRef(0);
  const selected = tools.find((t) => t.name === toolName);
  useEffect(
    () => () => {
      generation.current++;
      void client.current?.close().catch(() => {});
    },
    [],
  );
  const disconnect = async () => {
    generation.current++;
    const old = client.current;
    client.current = null;
    setTools([]);
    setToolName(null);
    setToken("");
    setResult("");
    setMessage("");
    setBusy(false);
    await old?.close().catch(() => {});
  };
  return (
    <Stack gap="md">
      <Text size="sm">
        Test the deployed endpoint. This executes live tools; changes in this
        draft are applied after you export and deploy.
      </Text>
      <TextInput
        label="Deployed MCP URL"
        value={url}
        onChange={(e) => setURL(e.currentTarget.value)}
        disabled={!!client.current}
      />
      {!anonymous && (
        <PasswordInput
          label="API key or OAuth access token"
          description="Kept in memory for this connection. Never saved in the draft or export."
          value={token}
          onChange={(e) => setToken(e.currentTarget.value)}
          disabled={!!client.current}
        />
      )}
      <Group>
        <Button
          loading={busy && !client.current}
          disabled={!!client.current || (!anonymous && !token)}
          onClick={async () => {
            setBusy(true);
            setMessage("");
            const current = ++generation.current;
            let connection: Client | undefined;
            try {
              const { Client, StreamableHTTPClientTransport } =
                await import("@modelcontextprotocol/client");
              connection = new Client({
                name: "Hookway builder",
                version: "1",
              });
              const target = new URL(url);
              if (
                target.protocol !== "https:" &&
                !(
                  target.protocol === "http:" &&
                  ["localhost", "127.0.0.1", "[::1]"].includes(target.hostname)
                )
              )
                throw new Error("Use HTTPS, or localhost for development.");
              await connection.connect(
                new StreamableHTTPClientTransport(target, {
                  authProvider: anonymous
                    ? undefined
                    : { token: async () => token },
                  requestInit: { redirect: "error" },
                }),
              );
              const listed = await connection.listTools();
              if (current !== generation.current) {
                await connection.close();
                return;
              }
              client.current = connection;
              setTools(listed.tools);
              setToolName(listed.tools[0]?.name ?? null);
              setMessage(`Connected. ${listed.tools.length} permitted tools.`);
            } catch {
              await connection?.close().catch(() => {});
              if (current === generation.current)
                setMessage(
                  "Connection failed. Check the endpoint, credentials and allowed browser origin.",
                );
            } finally {
              if (current === generation.current) setBusy(false);
            }
          }}
        >
          Connect and discover
        </Button>
        <Button variant="subtle" onClick={() => void disconnect()}>
          Disconnect and clear
        </Button>
      </Group>
      {message && (
        <Text role="status" size="sm">
          {message}
        </Text>
      )}
      {client.current && (
        <>
          <Select
            label="Deployed tool"
            data={tools.map((t) => ({ value: t.name, label: t.name }))}
            value={toolName}
            onChange={(name) => {
              setToolName(name);
              setApproved(false);
              setResult("");
              setArgsText("{}");
            }}
          />
          {selected && <Text size="sm">{selected.description}</Text>}
          <Textarea
            label="Live tool arguments"
            description="JSON object sent to the deployed tool."
            value={argsText}
            error={argsError || undefined}
            onChange={(e) => setArgsText(e.currentTarget.value)}
            autosize
            minRows={4}
            maxRows={20}
          />
          {selected && selected.annotations?.readOnlyHint !== true && (
            <Checkbox
              label="I understand this live call can change upstream data"
              checked={approved}
              onChange={(e) => setApproved(e.currentTarget.checked)}
            />
          )}
          <Button
            loading={busy}
            disabled={
              !!argsError ||
              !selected ||
              (selected.annotations?.readOnlyHint !== true && !approved)
            }
            onClick={async () => {
              if (!client.current || !toolName || argsError) return;
              setBusy(true);
              setResult("");
              const current = generation.current;
              try {
                const response = await client.current.callTool(
                  {
                    name: toolName,
                    arguments: args,
                  },
                  { timeout: 125_000 },
                );
                if (current === generation.current)
                  setResult(JSON.stringify(response, null, 2));
              } catch {
                if (current === generation.current)
                  setMessage(
                    "Call failed. Check permissions, arguments and the gateway request logs.",
                  );
              } finally {
                if (current === generation.current) setBusy(false);
              }
            }}
          >
            Run live tool
          </Button>
        </>
      )}
      {result && (
        <Code
          block
          style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}
        >
          {result}
        </Code>
      )}
      <Text size="xs" c="dimmed">
        Connection URL: {endpoint || "Set the public URL first."} API keys use
        Authorization: Bearer. For OAuth, register this endpoint with your
        client and use its OAuth login. Native clients can test endpoints that
        your browser cannot reach.
      </Text>
    </Stack>
  );
}
