import { useEffect, useRef, useState } from "react";
import { useStore } from "zustand";
import {
  Accordion,
  Alert,
  Badge,
  Box,
  Button,
  Checkbox,
  Code,
  Divider,
  FileButton,
  Group,
  Modal,
  MultiSelect,
  Paper,
  Select,
  Stack,
  Stepper,
  Text,
  Textarea,
  TextInput,
  Title,
} from "@mantine/core";
import {
  OpenAPIImporter,
  checkDocument,
  MAX_OPENAPI_BYTES,
  type ImportDiagnostic,
  type ImportResult,
  type OpenAPIOperation,
  type OpenAPIReport,
} from "../shared/wasm/openapi";
import { SchemaForm } from "./SchemaForm";
import { schemaProperties, type JSONSchema } from "./nodeSchema";
import settingsSchema from "./openapiSettingsSchema.json";
import { createDraftStore, FieldDraftContext } from "./formDrafts";
import { CredentialsForm } from "./CredentialsPanel";
import { MCPAuthentication } from "./MCPAuthentication";
import { MCPTester } from "./MCPTester";
import { record } from "./WorkspaceEntries";
import { toYaml } from "./toYaml";
import { fromYaml } from "./fromYaml";
import { useGraphStore } from "./store";

const properties = schemaProperties(settingsSchema as JSONSchema);
const endpointProperties = schemaProperties(properties.endpoint);
const selectionSchema = record(properties.operations).items as JSONSchema;
const accessSchema = schemaProperties(selectionSchema).access;
type Choice = {
  method: string;
  path: string;
  name: string;
  description: string;
  credential?: string;
  access: Record<string, unknown>;
};
const operationKey = (op: { method: string; path: string }) =>
  `${op.method} ${op.path}`;
const hasErrors = (diagnostics: ImportDiagnostic[] | null | undefined) =>
  diagnostics?.some((d) => d.severity === "error") === true;
const steps = [
  "Import API",
  "Select operations",
  "Configure access",
  "Review & export",
  "Connect & test",
];
function initialSettings() {
  return {
    server: { addr: "127.0.0.1:8080", max_body_bytes: 1048576 } as Record<
      string,
      unknown
    >,
    endpoint: {
      path: "/mcp",
      public_url: "",
      auth: { api_keys: {} },
    } as Record<string, unknown>,
    upstream: { base_url: "", allow_private_targets: false } as Record<
      string,
      unknown
    >,
    credentials: {} as Record<string, unknown>,
  };
}

export function OpenAPIWizard({
  onClose,
  onExportWorkspace,
}: {
  onClose: () => void;
  onExportWorkspace: () => void;
}) {
  const [drafts] = useState(createDraftStore);
  return (
    <FieldDraftContext.Provider value={drafts}>
      <Wizard
        onClose={onClose}
        onExportWorkspace={onExportWorkspace}
        drafts={drafts}
      />
    </FieldDraftContext.Provider>
  );
}
function Wizard({
  onClose,
  onExportWorkspace,
  drafts,
}: {
  onClose: () => void;
  onExportWorkspace: () => void;
  drafts: ReturnType<typeof createDraftStore>;
}) {
  const [step, setStep] = useState(0);
  const [configured, setConfigured] = useState(false);
  const [source, setSource] = useState("");
  const [filename, setFilename] = useState("");
  const [report, setReport] = useState<OpenAPIReport>();
  const [choices, setChoices] = useState<Record<string, Choice>>({});
  const [settings, setSettings] = useState(initialSettings);
  const [result, setResult] = useState<ImportResult>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [query, setQuery] = useState("");
  const [method, setMethod] = useState<string | null>(null);
  const [discard, setDiscard] = useState(false);
  const [replace, setReplace] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const importer = useRef<OpenAPIImporter | null>(null);
  const revision = useRef(0);
  const body = useRef<HTMLDivElement>(null);
  const formDrafts = useStore(drafts, (s) => s.formDrafts);
  const invalidDrafts = Object.values(formDrafts ?? {}).some((d) => !!d.error);
  const operations = report?.operations ?? [];
  const selected = operations.filter((op) => choices[operationKey(op)]);
  const sourceBytes = new TextEncoder().encode(source).length;
  const nameError = (name: string) =>
    !/^[a-zA-Z0-9_-]{1,64}$/.test(name) || ["out", "on_error"].includes(name)
      ? "Use 1–64 letters, numbers, underscores or hyphens; out and on_error are reserved."
      : selected.filter((op) => choices[operationKey(op)].name === name)
            .length > 1
        ? "Another selected tool uses this name."
        : undefined;
  const badNames = selected.some(
    (op) => !!nameError(choices[operationKey(op)].name),
  );
  function engine() {
    return (importer.current ??= new OpenAPIImporter());
  }
  function cancel() {
    revision.current++;
    importer.current?.cancel();
    setBusy(false);
  }
  useEffect(
    () => () => {
      revision.current++;
      importer.current?.cancel();
    },
    [],
  );
  useEffect(() => {
    body.current
      ?.querySelector<HTMLElement>(`[data-step="${step}"] h2`)
      ?.focus();
  }, [step]);
  function changeSource(value: string) {
    cancel();
    setSource(value);
    setReport(undefined);
    setChoices({});
    setResult(undefined);
    setError("");
  }
  function changeSettings(patch: Partial<ReturnType<typeof initialSettings>>) {
    cancel();
    setSettings((s) => ({ ...s, ...patch }));
    setResult(undefined);
    setError("");
  }
  function changeChoice(key: string, patch: Partial<Choice>) {
    cancel();
    setChoices((s) => ({ ...s, [key]: { ...s[key], ...patch } }));
    setResult(undefined);
    setError("");
  }
  async function inspect() {
    cancel();
    const current = revision.current;
    setBusy(true);
    setError("");
    try {
      const report = await engine().inspect(source);
      if (current !== revision.current) return;
      setReport(report);
      if (report.operations?.length) setStep(1);
      else
        setError(
          "No operations are available. Review the document diagnostics below.",
        );
    } catch (e) {
      if (current === revision.current)
        setError(e instanceof Error ? e.message : "Inspection failed.");
    } finally {
      if (current === revision.current) setBusy(false);
    }
  }
  async function generate() {
    if (invalidDrafts || badNames || !selected.length) {
      setError(
        "Repair invalid fields and select at least one operation before reviewing.",
      );
      return;
    }
    cancel();
    const current = revision.current;
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const result = await engine().generate(
        source,
        toYaml({
          ...settings,
          operations: selected.map((op) => choices[operationKey(op)]),
        }),
      );
      if (current !== revision.current) return;
      setResult(result);
      setStep(3);
    } catch (e) {
      if (current === revision.current)
        setError(e instanceof Error ? e.message : "Generation failed.");
    } finally {
      if (current === revision.current) setBusy(false);
    }
  }
  function focusIssue(d: ImportDiagnostic) {
    const match = /^operations\[(\d+)\]/.exec(d.location);
    const key =
      d.operation ||
      (match && selected[Number(match[1])]
        ? operationKey(selected[Number(match[1])])
        : "");
    const nextStep = d.message.includes("tool name") ? 1 : 2;
    setQuery("");
    setMethod(null);
    setStep(nextStep);
    requestAnimationFrame(() => {
      const section = body.current?.querySelector<HTMLElement>(
        `[data-step="${nextStep}"]`,
      );
      const target = key
        ? Array.from(
            section?.querySelectorAll<HTMLElement>("[data-operation]") ?? [],
          ).find((e) => e.dataset.operation === key)
        : section;
      const field = target?.querySelector<HTMLElement>(
        "[aria-invalid=true], input, textarea, button",
      );
      field?.focus();
      field?.scrollIntoView({ block: "center" });
    });
  }
  function download() {
    if (!result?.yaml || hasErrors(result.diagnostics)) return;
    const url = URL.createObjectURL(
      new Blob([result.yaml], { type: "application/yaml" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "core.yaml";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
  function openInBuilder() {
    if (!result?.yaml || hasErrors(result.diagnostics)) return;
    setPreparing(true);
    setError("");
    try {
      const doc = fromYaml(result.yaml);
      doc.nodes.forEach((node) => {
        node.data.literalDollars = true;
      });
      doc.literalConfigFields = ["server", "credentials"];
      useGraphStore.getState().setGraph(doc);
      onClose();
    } catch (e) {
      setReplace(false);
      setError(
        `Could not open in builder: ${e instanceof Error ? e.message : "unrepresentable schema"}. Your workspace is unchanged. Download remains available.`,
      );
    } finally {
      setPreparing(false);
    }
  }
  const complete = !!result?.yaml && !hasErrors(result.diagnostics);
  const close = () => {
    cancel();
    if (source || selected.length) setDiscard(true);
    else onClose();
  };
  const diagnostics = (
    values: ImportDiagnostic[] | null | undefined,
    repair = false,
  ) =>
    values?.map((d, i) => (
      <Alert
        key={i}
        color={d.severity === "error" ? "red" : "blue"}
        title={`${d.operation ? d.operation + " · " : ""}${d.location}`}
      >
        {d.message}
        {repair && (
          <Button
            size="compact-xs"
            variant="subtle"
            ml="sm"
            onClick={() => focusIssue(d)}
          >
            Review field
          </Button>
        )}
      </Alert>
    ));
  return (
    <>
      <Modal
        opened
        onClose={close}
        title="Create MCP tools from OpenAPI"
        size="min(1100px, 100vw - 32px)"
        closeButtonProps={{ "aria-label": "Close OpenAPI wizard" }}
      >
        <Stack gap="lg" ref={body}>
          <Text size="sm" c="dimmed">
            Create a standalone Hookway configuration. Your API document is
            processed in this browser.
          </Text>
          <Stepper
            active={step}
            size="xs"
            allowNextStepsSelect={false}
            onStepClick={busy ? undefined : setStep}
          >
            {steps.map((label) => (
              <Stepper.Step key={label} label={label} />
            ))}
          </Stepper>
          {error && (
            <Alert color="red" title="Import needs attention" role="alert">
              {error}
              <Text size="xs" mt="xs">
                If local processing is unavailable, retry or use{" "}
                <Code>hookway import-openapi</Code>.
              </Text>
            </Alert>
          )}
          {busy && (
            <Group>
              <Text role="status" size="sm">
                Processing locally…
              </Text>
              <Button variant="default" size="xs" onClick={cancel}>
                Cancel processing
              </Button>
            </Group>
          )}
          <Box hidden={step !== 0} data-step="0">
            <Stack>
              <Title order={2} size="h3" tabIndex={-1}>
                Import your API
              </Title>
              <Text size="sm">
                OpenAPI 3.0 or 3.1, JSON or YAML, up to 8 MiB. Same-document
                references only.
              </Text>
              <FileButton
                accept=".json,.yaml,.yml,application/json,application/yaml"
                onChange={async (file) => {
                  if (!file) return;
                  cancel();
                  const current = revision.current;
                  if (file.size > MAX_OPENAPI_BYTES) {
                    setError("The selected document exceeds 8 MiB.");
                    return;
                  }
                  try {
                    const text = new TextDecoder("utf-8", {
                      fatal: true,
                    }).decode(await file.arrayBuffer());
                    if (current !== revision.current) return;
                    checkDocument(text);
                    changeSource(text);
                    setFilename(file.name);
                  } catch {
                    if (current === revision.current)
                      setError(
                        "Choose a valid UTF-8 JSON or YAML file of at most 8 MiB.",
                      );
                  }
                }}
              >
                {(props) => (
                  <Button
                    {...props}
                    variant="default"
                    style={{ alignSelf: "flex-start" }}
                  >
                    Choose OpenAPI file
                  </Button>
                )}
              </FileButton>
              {filename && (
                <Text size="xs" c="dimmed">
                  {filename}
                </Text>
              )}
              <Textarea
                label="OpenAPI document"
                description="Paste the original document. No URLs are fetched."
                value={source}
                onChange={(e) => {
                  changeSource(e.currentTarget.value);
                  setFilename("");
                }}
                autosize
                minRows={10}
                maxRows={18}
                styles={{ input: { fontFamily: "monospace" } }}
              />
              <Text
                size="xs"
                c={sourceBytes > MAX_OPENAPI_BYTES ? "red" : "dimmed"}
              >
                {sourceBytes.toLocaleString()} /{" "}
                {MAX_OPENAPI_BYTES.toLocaleString()} bytes
              </Text>
              {diagnostics(report?.diagnostics)}
              <Button
                disabled={
                  busy || !source.trim() || sourceBytes > MAX_OPENAPI_BYTES
                }
                onClick={() => void inspect()}
                style={{ alignSelf: "flex-end" }}
              >
                Inspect operations
              </Button>
            </Stack>
          </Box>
          <Box hidden={step !== 1} data-step="1">
            <Stack>
              <Title order={2} size="h3" tabIndex={-1}>
                Choose the tools to expose
              </Title>
              <Text size="sm">
                {selected.length} / 64 selected. Each selected operation becomes
                one tool. Writes require separate mutation permission.
              </Text>
              {diagnostics(report?.diagnostics)}
              <Group grow>
                <TextInput
                  label="Search operations"
                  value={query}
                  onChange={(e) => setQuery(e.currentTarget.value)}
                />
                <Select
                  label="HTTP method"
                  clearable
                  placeholder="All methods"
                  data={[...new Set(operations.map((op) => op.method))]}
                  value={method}
                  onChange={setMethod}
                />
              </Group>
              {operations
                .filter(
                  (op) =>
                    (!method || method === op.method) &&
                    `${operationKey(op)} ${op.operation_id} ${op.name}`
                      .toLowerCase()
                      .includes(query.toLowerCase()),
                )
                .map((op) => {
                  const key = operationKey(op),
                    choice = choices[key],
                    unsupported = hasErrors(op.diagnostics);
                  return (
                    <Paper key={key} withBorder p="md" data-operation={key}>
                      <Stack gap="sm">
                        <Group justify="space-between">
                          <Checkbox
                            label={key}
                            checked={!!choice}
                            disabled={
                              unsupported || (!choice && selected.length >= 64)
                            }
                            onChange={(e) => {
                              cancel();
                              if (e.currentTarget.checked)
                                setChoices((s) => ({
                                  ...s,
                                  [key]: {
                                    method: op.method,
                                    path: op.path,
                                    name: op.name,
                                    description: op.description,
                                    access: {},
                                  },
                                }));
                              else
                                setChoices((s) => {
                                  const next = { ...s };
                                  delete next[key];
                                  return next;
                                });
                              setResult(undefined);
                            }}
                          />
                          <Badge
                            color={
                              unsupported
                                ? "gray"
                                : op.method === "GET"
                                  ? "blue"
                                  : "orange"
                            }
                          >
                            {unsupported
                              ? "Unsupported"
                              : op.method === "GET"
                                ? "Read only"
                                : "Changes data"}
                          </Badge>
                        </Group>
                        <Text size="sm">{op.summary || op.description}</Text>
                        <Text size="xs" c="dimmed">
                          Operation ID: {op.operation_id || "Not provided"} ·
                          Proposed tool: {op.name}
                        </Text>
                        <Security op={op} />
                        {diagnostics(op.diagnostics)}
                        {choice && (
                          <>
                            <TextInput
                              label={`Tool name for ${key}`}
                              value={choice.name}
                              error={nameError(choice.name)}
                              onChange={(e) =>
                                changeChoice(key, {
                                  name: e.currentTarget.value,
                                })
                              }
                            />
                            <Textarea
                              label={`Description for ${key}`}
                              value={choice.description}
                              onChange={(e) =>
                                changeChoice(key, {
                                  description: e.currentTarget.value,
                                })
                              }
                            />
                          </>
                        )}
                      </Stack>
                    </Paper>
                  );
                })}
            </Stack>
          </Box>
          <Box hidden={step !== 2} data-step="2">
            {configured && (
              <Stack gap="lg">
                <Title order={2} size="h3" tabIndex={-1}>
                  Configure upstream and caller access
                </Title>
                <Paper withBorder p="md">
                  <Stack>
                    <Title order={3} size="h4">
                      Upstream API
                    </Title>
                    <SchemaForm
                      schema={properties.upstream}
                      value={settings.upstream}
                      onChange={(value) =>
                        changeSettings({ upstream: record(value) })
                      }
                      scope="wizard/upstream"
                      labels={{
                        base_url: "Upstream base URL",
                        allow_private_targets: "Allow private upstream targets",
                      }}
                    />
                    <Text size="sm" c="dimmed">
                      The base URL is supplied by you. Credentials below
                      authenticate Hookway to this API.
                    </Text>
                    <CredentialsForm
                      value={settings.credentials}
                      onChange={(credentials) =>
                        changeSettings({ credentials })
                      }
                    />
                  </Stack>
                </Paper>
                <Paper withBorder p="md">
                  <Stack>
                    <Title order={3} size="h4">
                      MCP endpoint and callers
                    </Title>
                    <SchemaForm
                      schema={{
                        type: "object",
                        properties: endpointProperties,
                        required: ["path", "public_url"],
                      }}
                      hideFields={["auth"]}
                      value={settings.endpoint}
                      onChange={(endpoint) =>
                        changeSettings({ endpoint: record(endpoint) })
                      }
                      scope="wizard/endpoint"
                      labels={{
                        path: "MCP path",
                        public_url: "Public MCP URL",
                      }}
                    />
                    <MCPAuthentication
                      requireAuth
                      value={record(settings.endpoint.auth)}
                      onChange={(auth) =>
                        changeSettings({
                          endpoint: { ...settings.endpoint, auth },
                        })
                      }
                      scope="wizard/auth"
                    />
                    <Text size="sm" c="dimmed">
                      Caller authentication is separate from upstream
                      credentials. Grant each tool explicitly below.
                    </Text>
                  </Stack>
                </Paper>
                {selected.map((op) => {
                  const key = operationKey(op),
                    choice = choices[key];
                  const keys = Object.keys(
                    record(record(settings.endpoint.auth).api_keys),
                  );
                  return (
                    <Paper key={key} withBorder p="md" data-operation={key}>
                      <Stack>
                        <Title order={3} size="h4">
                          {choice.name}
                        </Title>
                        <Text size="sm">{key}</Text>
                        <Security op={op} />
                        <Select
                          label={`Upstream credential for ${key}`}
                          placeholder={
                            op.requires_auth
                              ? "Select a required profile"
                              : "No upstream credential"
                          }
                          clearable
                          data={Object.keys(settings.credentials)}
                          value={choice.credential || null}
                          onChange={(credential) =>
                            changeChoice(key, {
                              credential: credential || undefined,
                            })
                          }
                        />
                        <MultiSelect
                          searchable
                          label={`API key grants for ${key}`}
                          description="Only these named callers can discover and invoke this tool."
                          data={keys}
                          value={(choice.access.api_keys as string[]) ?? []}
                          onChange={(api_keys) =>
                            changeChoice(key, {
                              access: { ...choice.access, api_keys },
                            })
                          }
                        />
                        {!!record(settings.endpoint.auth).oauth && (
                          <SchemaForm
                            schema={accessSchema}
                            hideFields={[
                              "api_keys",
                              "allow_mutation",
                              "anonymous",
                            ]}
                            value={choice.access}
                            scope={`wizard/access/${key}`}
                            onChange={(access) =>
                              changeChoice(key, { access: record(access) })
                            }
                          />
                        )}
                        {op.method !== "GET" && (
                          <Checkbox
                            label={`Allow mutation for ${key}`}
                            checked={choice.access.allow_mutation === true}
                            onChange={(e) =>
                              changeChoice(key, {
                                access: {
                                  ...choice.access,
                                  allow_mutation: e.currentTarget.checked,
                                },
                              })
                            }
                          />
                        )}
                      </Stack>
                    </Paper>
                  );
                })}
                <Accordion variant="contained">
                  <Accordion.Item value="server">
                    <Accordion.Control>
                      Advanced server settings
                    </Accordion.Control>
                    <Accordion.Panel>
                      <SchemaForm
                        schema={properties.server}
                        value={settings.server}
                        onChange={(server) =>
                          changeSettings({ server: record(server) })
                        }
                        scope="wizard/server"
                      />
                    </Accordion.Panel>
                  </Accordion.Item>
                </Accordion>
              </Stack>
            )}
          </Box>
          <Box hidden={step !== 3} data-step="3">
            <Stack>
              <Title order={2} size="h3" tabIndex={-1}>
                Review generated tools
              </Title>
              {diagnostics(result?.diagnostics, true)}
              {!result && (
                <Text>
                  Return to Configure access and generate a current review.
                </Text>
              )}
              {complete && (
                <>
                  <Alert color="green" title="Configuration validated">
                    {selected.length} tools · one MCP endpoint · no automatic
                    retries. Deployment applies this configuration.
                  </Alert>
                  {selected.map((op) => {
                    const choice = choices[operationKey(op)];
                    return (
                      <Paper withBorder p="md" key={operationKey(op)}>
                        <Stack gap="xs">
                          <Group justify="space-between">
                            <Text fw={650}>{choice.name}</Text>
                            <Badge
                              color={op.method === "GET" ? "blue" : "orange"}
                            >
                              {op.method === "GET"
                                ? "Read only"
                                : "Mutation enabled"}
                            </Badge>
                          </Group>
                          <Text size="sm">
                            {choice.description || op.description}
                          </Text>
                          <Code>{`MCP ${choice.name} → ${operationKey(op)} → result {data: …}`}</Code>
                          <Text size="xs">
                            Credential: {choice.credential || "None"} · Grants:{" "}
                            {JSON.stringify(choice.access)}
                          </Text>
                          {Object.keys(op.aliases ?? {}).length > 0 && (
                            <Text size="xs">
                              Argument aliases: {JSON.stringify(op.aliases)}
                            </Text>
                          )}
                          <Accordion>
                            <Accordion.Item value="schemas">
                              <Accordion.Control>
                                Arguments and result schemas
                              </Accordion.Control>
                              <Accordion.Panel>
                                <Stack>
                                  <Text size="sm">
                                    Arguments use path, query, headers, and body
                                    groups when declared.
                                  </Text>
                                  <Code block>{op.input_schema_json}</Code>
                                  <Text size="sm">Output schema</Text>
                                  <Code block>{op.output_schema_json}</Code>
                                </Stack>
                              </Accordion.Panel>
                            </Accordion.Item>
                          </Accordion>
                        </Stack>
                      </Paper>
                    );
                  })}
                  <Textarea
                    label="Generated core.yaml"
                    readOnly
                    value={result.yaml}
                    autosize
                    minRows={8}
                    maxRows={18}
                    styles={{ input: { fontFamily: "monospace" } }}
                  />
                  <Group>
                    <Button onClick={download}>Download core.yaml</Button>
                    <Button variant="default" onClick={() => setReplace(true)}>
                      Open in builder
                    </Button>
                  </Group>
                </>
              )}
            </Stack>
          </Box>
          <Box hidden={step !== 4} data-step="4">
            <Stack>
              <Title order={2} size="h3" tabIndex={-1}>
                Deploy, then connect
              </Title>
              <Text size="sm">
                Download core.yaml, provision its referenced secrets on your
                Hookway host, then validate and serve it:
              </Text>
              <Code block>
                {
                  "hookway validate --config core.yaml\nhookway serve --config core.yaml"
                }
              </Code>
              <Text size="sm">
                Configure your public URL and browser origin for the deployment.
                This wizard does not deploy or start a server. The live tool
                catalog belongs to that running endpoint and may differ from
                this draft.
              </Text>
              <Button
                onClick={download}
                disabled={!complete}
                style={{ alignSelf: "flex-start" }}
              >
                Download core.yaml
              </Button>
              {complete && step === 4 && (
                <MCPTester
                  key={String(settings.endpoint.public_url)}
                  endpoint={String(settings.endpoint.public_url || "")}
                  anonymous={false}
                />
              )}
            </Stack>
          </Box>
          <Divider />
          <Group
            justify="space-between"
            style={{
              position: "sticky",
              bottom: 0,
              background: "var(--mantine-color-body)",
              paddingBlock: 12,
              zIndex: 1,
              borderTop: "1px solid var(--mantine-color-default-border)",
            }}
          >
            <Button
              variant="default"
              onClick={() => setStep((s) => Math.max(0, s - 1))}
              disabled={step === 0 || busy}
            >
              Back
            </Button>
            {step === 1 && (
              <Button
                disabled={!selected.length || badNames || busy}
                onClick={() => {
                  setConfigured(true);
                  setStep(2);
                }}
              >
                Configure access
              </Button>
            )}
            {step === 2 && (
              <Button
                disabled={!selected.length || badNames || invalidDrafts || busy}
                onClick={() => void generate()}
              >
                Generate review
              </Button>
            )}
            {step === 3 && (
              <Button disabled={!complete} onClick={() => setStep(4)}>
                Connect & test
              </Button>
            )}
            {step === 4 && (
              <Button variant="default" onClick={close}>
                Finish
              </Button>
            )}
          </Group>
        </Stack>
      </Modal>
      <Modal
        opened={discard}
        onClose={() => setDiscard(false)}
        title="Discard this import?"
        size="sm"
      >
        <Stack>
          <Text size="sm">
            The API document and wizard draft are kept only for this session.
            Your current builder workspace will remain unchanged.
          </Text>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setDiscard(false)}>
              Keep editing
            </Button>
            <Button color="red" onClick={onClose}>
              Discard import
            </Button>
          </Group>
        </Stack>
      </Modal>
      <Modal
        opened={replace}
        onClose={() => setReplace(false)}
        title="Replace the builder workspace?"
        size="sm"
      >
        <Stack>
          <Text size="sm">
            Opening this configuration replaces all current flows, settings, and
            replay configuration. Export the current workspace first if you need
            a copy.
          </Text>
          <Button variant="default" onClick={onExportWorkspace}>
            Export current workspace
          </Button>
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setReplace(false)}>
              Cancel replacement
            </Button>
            <Button loading={preparing} onClick={openInBuilder}>
              Replace workspace
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  );
}
function Security({ op }: { op: OpenAPIOperation }) {
  const requirements = op.security_requirements
    ?.map((group) =>
      group?.length
        ? group
            .map(
              (r) =>
                `${r.name} (${r.type}${r.header ? `: ${r.header}` : r.scheme ? `: ${r.scheme}` : ""}${r.scopes?.length ? `; scopes ${r.scopes.join(", ")}` : ""})`,
            )
            .join(" + ")
        : "No authentication",
    )
    .join(" OR ");
  return (
    <Text size="xs" c="dimmed">
      Upstream authentication: {requirements || "None declared"}
    </Text>
  );
}
