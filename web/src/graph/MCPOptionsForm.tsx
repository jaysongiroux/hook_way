import { useEffect, useRef } from "react";
import {
  Alert,
  Button,
  Group,
  Stack,
  Tabs,
  Text,
  TextInput,
} from "@mantine/core";
import { MCPEventsForm } from "./MCPEventsForm";
import { MCPTester } from "./MCPTester";
import { Fields, object, type OptionsProps } from "./FormPresentation";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";
import { MCPAuthentication } from "./MCPAuthentication";
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
        <Tabs.Tab value="events">Events ({Array.isArray(value.events) ? value.events.length : 0})</Tabs.Tab>
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
          <MCPAuthentication value={auth} onChange={(auth) => onChange({ ...value, auth })} scope={scope} />
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
      <Tabs.Panel value="events" pt="md"><MCPEventsForm value={value} onChange={onChange} scope={scope} /></Tabs.Panel>
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
