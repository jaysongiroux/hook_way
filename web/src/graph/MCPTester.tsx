import { useEffect, useRef, useState } from "react";
import {
  Button,
  Checkbox,
  Code,
  Group,
  PasswordInput,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import type { Client, Tool } from "@modelcontextprotocol/client";
export function MCPTester({
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
              const { Client, StreamableHTTPClientTransport } = await import(
                "@modelcontextprotocol/client"
              );
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
                  requestInit: { redirect: "error", credentials: "omit" },
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
          role="region"
          aria-label="Live tool result"
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
