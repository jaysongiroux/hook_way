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
  FileButton,
  Group,
  Modal,
  MultiSelect,
  Select,
  Stack,
  Stepper,
  Tabs,
  Progress,
  Loader,
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
import { ExportModal } from "./ExportModal";
import { MCPTester } from "./MCPTester";
import { record } from "./WorkspaceEntries";
import { toYaml } from "./toYaml";
import {
  FiUploadCloud,
  FiFileText,
  FiSearch,
  FiArrowRight,
  FiCheckCircle,
  FiShield,
} from "react-icons/fi";
import classes from "./OpenAPIWizard.module.css";
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
const steps = ["API document", "Tools", "Access", "Export"];
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

export function OpenAPIWizard({ onClose }: { onClose: () => void }) {
  const [drafts] = useState(createDraftStore);
  return (
    <FieldDraftContext.Provider value={drafts}>
      <Wizard onClose={onClose} drafts={drafts} />
    </FieldDraftContext.Provider>
  );
}
function Wizard({
  onClose,
  drafts,
}: {
  onClose: () => void;
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
  const [exporting, setExporting] = useState(false);
  const [sourceMode, setSourceMode] = useState<string | null>("upload");
  const [accessPanel, setAccessPanel] = useState<string | null>("api");
  const [editOperations, setEditOperations] = useState<string[]>([]);
  const [advancedOpened, setAdvancedOpened] = useState<string[]>([]);
  const [endpointOpened, setEndpointOpened] = useState<string | null>(null);
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
  function advanceAccess(panel: string) {
    setAccessPanel(panel);
    requestAnimationFrame(() => {
      body.current
        ?.querySelector<HTMLElement>(
          '[data-step="2"] [role="tab"][aria-selected="true"]',
        )
        ?.focus();
    });
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
    const tool = selected.find((op) =>
      d.message.includes(JSON.stringify(choices[operationKey(op)].name)),
    );
    const key =
      d.operation ||
      (match && selected[Number(match[1])]
        ? operationKey(selected[Number(match[1])])
        : tool
          ? operationKey(tool)
          : "");
    const nextStep = /tool name|description/i.test(d.message) ? 1 : 2;
    const [group, ...parts] = d.location.split(".");
    if (nextStep === 1 && key)
      setEditOperations((items) => [...new Set([...items, key])]);
    if (nextStep === 2) {
      setAccessPanel(
        key ? "permissions" : group === "endpoint" ? "callers" : "api",
      );
      if (group === "credentials") setAdvancedOpened(["credentials"]);
      if (
        group === "endpoint" &&
        !["path", "public_url", "auth"].includes(parts[0])
      )
        setEndpointOpened("advanced");
    }
    if (nextStep === 2) setConfigured(true);
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
        : (Array.from(
            section?.querySelectorAll<HTMLElement>("[data-import-section]") ??
              [],
          ).find((e) => e.dataset.importSection === group) ?? section);
      const path = key
        ? /allow_mutation/.test(d.message)
          ? "access.allow_mutation"
          : /credential/.test(d.message)
            ? "credential"
            : nextStep === 1
              ? /description/.test(d.message)
                ? "description"
                : "name"
              : "access"
        : group === "credentials"
          ? d.location
          : parts.join(".");
      const field =
        Array.from(
          target?.querySelectorAll<HTMLElement>("[data-field-path]") ?? [],
        ).find((e) => e.dataset.fieldPath === path) ??
        target?.querySelector<HTMLElement>(
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
      for (const node of doc.nodes) {
        if (
          node.data.nodeType !== "mcp_trigger" ||
          !Array.isArray(node.data.options.tools)
        )
          continue;
        const paths = node.data.literalDollarPaths ?? [];
        node.data.options.tools.forEach((_, index) => {
          paths.push(
            ["tools", index, "input_schema"],
            ["tools", index, "output_schema"],
          );
        });
        node.data.literalDollarPaths = paths;
      }
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
        size="min(960px, 100vw - 24px)"
        styles={{ body: { padding: 0 } }}
        classNames={{ body: classes.modalBody, header: classes.modalHeader }}
        closeButtonProps={{ "aria-label": "Close OpenAPI wizard" }}
      >
        <Stack gap={0} ref={body} className={classes.wizard}>
          <Box className={classes.progress}>
            <Stepper
              active={Math.min(step, 4)}
              size="xs"
              iconSize={26}
              allowNextStepsSelect={false}
              onStepClick={busy ? undefined : setStep}
              visibleFrom="sm"
            >
              {steps.map((label) => (
                <Stepper.Step key={label} label={label} />
              ))}
            </Stepper>
            <Box hiddenFrom="sm">
              <Group justify="space-between" mb="xs">
                <Text size="sm" fw={600}>
                  {step === 4 ? "Test your endpoint" : steps[step]}
                </Text>
                <Text size="xs" c="dimmed">
                  {step === 4 ? "Optional" : `Step ${step + 1} of 4`}
                </Text>
              </Group>
              <Progress
                value={Math.min(step + 1, 4) * 25}
                size={3}
                aria-label="Import progress"
              />
            </Box>
          </Box>
          <Box className={classes.content}>
            {error && (
              <Alert
                color="red"
                title="Import needs attention"
                role="alert"
                mb="md"
              >
                {error}
                {error.includes("WASM") && (
                  <Text size="sm" mt="xs">
                    Try again, or use <Code>hookway import-openapi</Code> from
                    your terminal.
                  </Text>
                )}
              </Alert>
            )}
            {busy && (
              <Group mb="md" role="status">
                <Loader size="xs" />
                <Text size="sm">
                  {step === 0
                    ? "Reading your API document…"
                    : "Validating your configuration…"}
                </Text>
                <Button variant="subtle" size="compact-sm" onClick={cancel}>
                  Cancel processing
                </Button>
              </Group>
            )}
            <Box hidden={step !== 0} data-step="0">
              <Stack gap="lg">
                <StepHeading
                  title="Turn your API into agent tools"
                  description="Start with an OpenAPI document. You’ll choose which operations agents can use."
                />
                <Tabs value={sourceMode} onChange={setSourceMode} keepMounted>
                  <Tabs.List mb="lg">
                    <Tabs.Tab
                      value="upload"
                      leftSection={<FiUploadCloud aria-hidden />}
                    >
                      Upload file
                    </Tabs.Tab>
                    <Tabs.Tab
                      value="paste"
                      leftSection={<FiFileText aria-hidden />}
                    >
                      Paste document
                    </Tabs.Tab>
                  </Tabs.List>
                  <Tabs.Panel value="upload">
                    <Box className={classes.upload}>
                      {filename ? (
                        <FiFileText size={32} aria-hidden />
                      ) : (
                        <FiUploadCloud size={32} aria-hidden />
                      )}
                      <Text fw={600} mt="md" className={classes.breakText}>
                        {filename || "Choose your OpenAPI document"}
                      </Text>
                      <Text size="sm" c="dimmed" mt={4}>
                        {filename
                          ? `${(sourceBytes / 1024).toFixed(1)} KB · Ready to inspect`
                          : "OpenAPI 3.0 or 3.1 · JSON or YAML · Up to 8 MiB"}
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
                          <Button {...props} variant="default" mt="lg">
                            {filename ? "Change file" : "Choose OpenAPI file"}
                          </Button>
                        )}
                      </FileButton>
                    </Box>
                  </Tabs.Panel>
                  <Tabs.Panel value="paste">
                    <Textarea
                      label="OpenAPI document"
                      placeholder="openapi: 3.1.0"
                      value={source}
                      onChange={(e) => {
                        changeSource(e.currentTarget.value);
                        setFilename("");
                      }}
                      autosize
                      minRows={9}
                      maxRows={14}
                      className={classes.codeInput}
                    />
                    <Text
                      size="xs"
                      c={sourceBytes > MAX_OPENAPI_BYTES ? "red" : "dimmed"}
                      mt="xs"
                    >
                      {sourceBytes > MAX_OPENAPI_BYTES
                        ? "Document exceeds the 8 MiB limit."
                        : "JSON or YAML · Up to 8 MiB"}
                    </Text>
                  </Tabs.Panel>
                </Tabs>
                <Group gap="xs" c="dimmed">
                  <FiShield aria-hidden />
                  <Text size="sm">
                    Processed in your browser. Your document stays on this
                    device.
                  </Text>
                </Group>
                {diagnostics(report?.diagnostics)}
              </Stack>
            </Box>
            <Box hidden={step !== 1} data-step="1">
              <Stack gap="lg">
                <StepHeading
                  title="Choose what agents can do"
                  description="Only selected operations become tools. Choose what agents need to use."
                />
                {diagnostics(report?.diagnostics)}
                <Group align="end" className={classes.filters}>
                  <TextInput
                    label="Search operations"
                    placeholder="Search by path or tool name"
                    leftSection={<FiSearch aria-hidden />}
                    value={query}
                    onChange={(e) => setQuery(e.currentTarget.value)}
                    className={classes.search}
                  />
                  <Select
                    label="HTTP method"
                    clearable
                    placeholder="All methods"
                    data={[...new Set(operations.map((op) => op.method))]}
                    value={method}
                    onChange={setMethod}
                    className={classes.methodFilter}
                  />
                </Group>
                <Group justify="space-between">
                  <Text size="sm" fw={600}>
                    {operations.length} operations
                  </Text>
                  <Text size="sm" c="dimmed" aria-live="polite">
                    {selected.length} of 64 selected
                  </Text>
                </Group>
                <Box className={classes.operationList}>
                  {operations
                    .filter(
                      (op) =>
                        (!method || method === op.method) &&
                        `${operationKey(op)} ${op.operation_id} ${op.name} ${op.summary}`
                          .toLowerCase()
                          .includes(query.toLowerCase()),
                    )
                    .map((op) => {
                      const key = operationKey(op),
                        choice = choices[key],
                        unsupported = hasErrors(op.diagnostics);
                      return (
                        <Box
                          key={key}
                          className={classes.operation}
                          data-operation={key}
                          data-selected={!!choice}
                        >
                          <Group align="start" wrap="nowrap" gap="md">
                            <Checkbox
                              aria-label={key}
                              mt={4}
                              checked={!!choice}
                              disabled={
                                unsupported ||
                                (!choice && selected.length >= 64)
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
                            <Stack gap={5} className={classes.operationInfo}>
                              <Group gap="sm">
                                <Method method={op.method} />
                                <Text
                                  size="sm"
                                  fw={600}
                                  className={classes.path}
                                >
                                  {op.path}
                                </Text>
                                {unsupported ? (
                                  <Badge color="gray" variant="light">
                                    Unsupported
                                  </Badge>
                                ) : (
                                  op.method !== "GET" && (
                                    <Badge color="orange" variant="light">
                                      Changes data
                                    </Badge>
                                  )
                                )}
                              </Group>
                              <Text size="sm">
                                {op.summary || op.description || op.name}
                              </Text>
                              <Text size="xs" c="dimmed">
                                Tool:{" "}
                                <span className={classes.mono}>
                                  {choice?.name || op.name}
                                </span>
                                {op.operation_id && op.operation_id !== op.name
                                  ? ` · Operation ID: ${op.operation_id}`
                                  : ""}
                              </Text>
                              <Security op={op} />
                              {diagnostics(op.diagnostics)}
                              {choice && (
                                <Accordion
                                  variant="default"
                                  value={
                                    editOperations.includes(key) ? "edit" : null
                                  }
                                  onChange={(value) =>
                                    setEditOperations((items) =>
                                      value
                                        ? [...items, key]
                                        : items.filter((item) => item !== key),
                                    )
                                  }
                                  transitionDuration={0}
                                >
                                  <Accordion.Item value="edit">
                                    <Accordion.Control
                                      className={classes.disclosure}
                                    >
                                      {nameError(choice.name)
                                        ? "Fix tool name"
                                        : "Edit tool details"}
                                    </Accordion.Control>
                                    <Accordion.Panel>
                                      <Stack gap="sm">
                                        <TextInput
                                          data-field-path="name"
                                          label="Tool name"
                                          aria-label={`Tool name for ${key}`}
                                          value={choice.name}
                                          error={nameError(choice.name)}
                                          onChange={(e) =>
                                            changeChoice(key, {
                                              name: e.currentTarget.value,
                                            })
                                          }
                                        />
                                        <Textarea
                                          data-field-path="description"
                                          label="Description for agents"
                                          aria-label={`Description for ${key}`}
                                          description="Help agents understand when to use this tool."
                                          value={choice.description}
                                          onChange={(e) =>
                                            changeChoice(key, {
                                              description:
                                                e.currentTarget.value,
                                            })
                                          }
                                          autosize
                                          minRows={2}
                                          maxRows={5}
                                        />
                                      </Stack>
                                    </Accordion.Panel>
                                  </Accordion.Item>
                                </Accordion>
                              )}
                              {choice && nameError(choice.name) && (
                                <Text size="xs" c="red" role="alert">
                                  {nameError(choice.name)}
                                </Text>
                              )}
                            </Stack>
                          </Group>
                        </Box>
                      );
                    })}
                  {!operations.some(
                    (op) =>
                      (!method || method === op.method) &&
                      `${operationKey(op)} ${op.operation_id} ${op.name} ${op.summary}`
                        .toLowerCase()
                        .includes(query.toLowerCase()),
                  ) && (
                    <Text p="lg" c="dimmed" size="sm">
                      No operations match. Try another search or method.
                    </Text>
                  )}
                </Box>
              </Stack>
            </Box>
            <Box hidden={step !== 2} data-step="2">
              {configured && (
                <Stack gap="lg">
                  <StepHeading
                    title="Connect your API and choose access"
                    description="Hookway connects to your API using its own credentials. Agents authenticate separately."
                  />
                  <Tabs
                    value={accessPanel}
                    onChange={setAccessPanel}
                    keepMounted
                    classNames={{ list: classes.accessTabs }}
                  >
                    <Tabs.List mb="lg">
                      <Tabs.Tab value="api">Your API</Tabs.Tab>
                      <Tabs.Tab value="callers">Agent access</Tabs.Tab>
                      <Tabs.Tab value="permissions">Tool permissions</Tabs.Tab>
                    </Tabs.List>
                    <Tabs.Panel value="api">
                      <Stack gap="lg">
                        <Box data-import-section="upstream">
                          <Stack gap="sm">
                            <Text fw={600}>
                              Where should Hookway send requests?
                            </Text>
                            <SchemaForm
                              schema={properties.upstream}
                              value={settings.upstream}
                              onChange={(value) =>
                                changeSettings({ upstream: record(value) })
                              }
                              scope="wizard/upstream"
                              labels={{
                                base_url: "Upstream base URL",
                                allow_private_targets:
                                  "Allow private upstream targets",
                              }}
                            />
                            <Text size="xs" c="dimmed">
                              Include the API’s base path, such as
                              https://api.example.com/v1.
                            </Text>
                          </Stack>
                        </Box>
                        <Accordion
                          multiple
                          value={advancedOpened}
                          onChange={setAdvancedOpened}
                          transitionDuration={0}
                        >
                          <Accordion.Item
                            value="credentials"
                            data-import-section="credentials"
                          >
                            <Accordion.Control>
                              API credentials
                              {selected.some((op) => op.requires_auth)
                                ? " · Required by selected operations"
                                : " · Optional"}
                            </Accordion.Control>
                            <Accordion.Panel>
                              <Text size="sm" c="dimmed" mb="md">
                                Add the credentials Hookway needs to call your
                                API. You’ll assign profiles to tools under Tool
                                permissions.
                              </Text>
                              <CredentialsForm
                                value={settings.credentials}
                                onChange={(credentials) =>
                                  changeSettings({ credentials })
                                }
                              />
                            </Accordion.Panel>
                          </Accordion.Item>
                        </Accordion>
                      </Stack>
                    </Tabs.Panel>
                    <Tabs.Panel value="callers">
                      <Stack gap="lg" data-import-section="endpoint">
                        <Box>
                          <Text fw={600} mb="sm">
                            Where will agents connect?
                          </Text>
                          <SchemaForm
                            schema={{
                              type: "object",
                              properties: {
                                path: endpointProperties.path,
                                public_url: endpointProperties.public_url,
                              },
                              required: ["path", "public_url"],
                            }}
                            value={settings.endpoint}
                            onChange={(endpoint) =>
                              changeSettings({
                                endpoint: {
                                  ...settings.endpoint,
                                  ...record(endpoint),
                                },
                              })
                            }
                            scope="wizard/endpoint"
                            labels={{
                              path: "MCP path",
                              public_url: "Public MCP URL",
                            }}
                          />
                        </Box>
                        <Box>
                          <Text fw={600} mb="sm">
                            Who can connect?
                          </Text>
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
                        </Box>
                        <Accordion
                          value={endpointOpened}
                          onChange={setEndpointOpened}
                          transitionDuration={0}
                        >
                          <Accordion.Item value="advanced">
                            <Accordion.Control>
                              Browser origins & request limits
                            </Accordion.Control>
                            <Accordion.Panel>
                              <SchemaForm
                                schema={{
                                  type: "object",
                                  properties: endpointProperties,
                                }}
                                hideFields={["auth", "path", "public_url"]}
                                value={settings.endpoint}
                                onChange={(endpoint) =>
                                  changeSettings({ endpoint: record(endpoint) })
                                }
                                scope="wizard/endpoint"
                                labels={{
                                  allowed_origins: "Allowed browser origins",
                                }}
                              />
                            </Accordion.Panel>
                          </Accordion.Item>
                        </Accordion>
                      </Stack>
                    </Tabs.Panel>
                    <Tabs.Panel value="permissions">
                      <Stack gap="md">
                        <Text size="sm" c="dimmed">
                          Choose who can discover and run each tool. No access
                          is granted automatically.
                        </Text>
                        {selected.map((op) => {
                          const key = operationKey(op),
                            choice = choices[key],
                            keys = Object.keys(
                              record(record(settings.endpoint.auth).api_keys),
                            );
                          return (
                            <Box
                              key={key}
                              className={classes.permission}
                              data-operation={key}
                            >
                              <Stack gap="md">
                                <Group justify="space-between">
                                  <Box>
                                    <Text fw={600}>{choice.name}</Text>
                                    <Text size="xs" c="dimmed">
                                      {key}
                                    </Text>
                                  </Box>
                                  {op.method !== "GET" && (
                                    <Badge color="orange" variant="light">
                                      Changes data
                                    </Badge>
                                  )}
                                </Group>
                                {(op.requires_auth ||
                                  Object.keys(settings.credentials).length >
                                    0) && (
                                  <>
                                    <Security op={op} />
                                    <Select
                                      data-field-path="credential"
                                      label="API credential profile"
                                      aria-label={`Upstream credential for ${key}`}
                                      placeholder={
                                        op.requires_auth
                                          ? "Choose a required profile"
                                          : "No credential needed"
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
                                  </>
                                )}
                                {!!record(settings.endpoint.auth).api_keys && (
                                  <MultiSelect
                                    searchable
                                    data-field-path="access"
                                    label="Allowed callers"
                                    aria-label={`API key grants for ${key}`}
                                    placeholder={
                                      keys.length
                                        ? "Choose callers"
                                        : "Add a caller under Agent access first"
                                    }
                                    data={keys}
                                    value={
                                      (choice.access.api_keys as string[]) ?? []
                                    }
                                    onChange={(api_keys) =>
                                      changeChoice(key, {
                                        access: { ...choice.access, api_keys },
                                      })
                                    }
                                  />
                                )}
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
                                      changeChoice(key, {
                                        access: record(access),
                                      })
                                    }
                                  />
                                )}
                                {op.method !== "GET" && (
                                  <Checkbox
                                    data-field-path="access.allow_mutation"
                                    label="Allow this tool to change API data"
                                    aria-label={`Allow mutation for ${key}`}
                                    description={`${op.method} requests can modify or delete upstream data.`}
                                    checked={
                                      choice.access.allow_mutation === true
                                    }
                                    onChange={(e) =>
                                      changeChoice(key, {
                                        access: {
                                          ...choice.access,
                                          allow_mutation:
                                            e.currentTarget.checked,
                                        },
                                      })
                                    }
                                  />
                                )}
                              </Stack>
                            </Box>
                          );
                        })}
                      </Stack>
                    </Tabs.Panel>
                  </Tabs>
                </Stack>
              )}
            </Box>
            <Box hidden={step !== 3} data-step="3">
              <Stack gap="lg">
                <StepHeading
                  title={
                    complete
                      ? "Your tools are ready to export"
                      : "Review your configuration"
                  }
                  description={
                    complete
                      ? "Download the configuration to run with Hookway, or open it in the builder to make changes."
                      : "Resolve the issues below, then review again."
                  }
                />
                {diagnostics(result?.diagnostics, true)}
                {!result && (
                  <Text size="sm">
                    Go back to Access to review the latest configuration.
                  </Text>
                )}
                {complete && (
                  <>
                    <Group gap="xs" className={classes.validated}>
                      <FiCheckCircle aria-hidden />
                      <Text size="sm" fw={600}>
                        Configuration validated
                      </Text>
                      <Text size="sm">
                        · {selected.length}{" "}
                        {selected.length === 1 ? "tool" : "tools"} · 1 endpoint
                      </Text>
                    </Group>
                    <Box className={classes.route} aria-label="Request flow">
                      <span>Authenticated agent</span>
                      <FiArrowRight aria-hidden />
                      <strong>Hookway MCP</strong>
                      <FiArrowRight aria-hidden />
                      <span className={classes.breakText}>
                        {String(settings.upstream.base_url)}
                      </span>
                    </Box>
                    <Accordion multiple transitionDuration={0}>
                      {selected.map((op) => {
                        const choice = choices[operationKey(op)];
                        return (
                          <Accordion.Item
                            key={operationKey(op)}
                            value={operationKey(op)}
                          >
                            <Accordion.Control>
                              <Group justify="space-between" pr="sm">
                                <Text fw={600}>{choice.name}</Text>
                                <Badge
                                  variant="light"
                                  color={
                                    op.method === "GET" ? "gray" : "orange"
                                  }
                                >
                                  {op.method === "GET"
                                    ? "Read only"
                                    : "Can change data"}
                                </Badge>
                              </Group>
                              <Text size="sm" c="dimmed" mt={4}>
                                {operationKey(op)}
                              </Text>
                            </Accordion.Control>
                            <Accordion.Panel>
                              <Stack gap="md">
                                <Text size="sm">
                                  {choice.description || op.description}
                                </Text>
                                <Text size="sm">
                                  API credential:{" "}
                                  {choice.credential || "Not required"}
                                  <br />
                                  Allowed callers: {grantSummary(choice.access)}
                                </Text>
                                {Object.keys(op.aliases ?? {}).length > 0 && (
                                  <Box>
                                    <Text size="sm" fw={600}>
                                      Renamed arguments
                                    </Text>
                                    {Object.entries(op.aliases ?? {}).map(
                                      ([original, alias]) => (
                                        <Text key={original} size="sm">
                                          {original} → {String(alias)}
                                        </Text>
                                      ),
                                    )}
                                  </Box>
                                )}
                                <Text size="sm" fw={600}>
                                  Input schema
                                </Text>
                                <Text size="xs" c="dimmed">
                                  Arguments are grouped by path, query, headers,
                                  and body when present.
                                </Text>
                                <Code block>{op.input_schema_json}</Code>
                                <Text size="sm" fw={600}>
                                  Output schema
                                </Text>
                                <Code block>{op.output_schema_json}</Code>
                                <Text size="xs" c="dimmed">
                                  One HTTP request, followed by a validated
                                  result in the data field. Automatic retries
                                  are disabled.
                                </Text>
                              </Stack>
                            </Accordion.Panel>
                          </Accordion.Item>
                        );
                      })}
                      <Accordion.Item value="yaml">
                        <Accordion.Control>View core.yaml</Accordion.Control>
                        <Accordion.Panel>
                          <Textarea
                            label="Generated core.yaml"
                            readOnly
                            value={result.yaml}
                            autosize
                            minRows={8}
                            maxRows={16}
                            className={classes.codeInput}
                          />
                        </Accordion.Panel>
                      </Accordion.Item>
                    </Accordion>
                    <Button
                      variant="default"
                      onClick={() => setReplace(true)}
                      style={{ alignSelf: "flex-start" }}
                    >
                      Open in builder
                    </Button>
                    <Text size="sm" c="dimmed">
                      Nothing has been deployed. You can test a running endpoint
                      after downloading.
                    </Text>
                    <Button
                      variant="subtle"
                      onClick={() => setStep(4)}
                      style={{ alignSelf: "flex-start" }}
                    >
                      Connect & test{" "}
                      <Text span size="xs" ml="xs" c="dimmed">
                        Optional
                      </Text>
                    </Button>
                  </>
                )}
              </Stack>
            </Box>
            <Box hidden={step !== 4} data-step="4">
              <Stack gap="lg">
                <StepHeading
                  title="Test a running endpoint"
                  description="Connect to a deployed Hookway server to see and run the tools your caller can access."
                />
                <Text size="sm" c="dimmed">
                  The live catalog may differ from this configuration.
                  Connecting doesn’t deploy your changes.
                </Text>
                <Accordion transitionDuration={0}>
                  <Accordion.Item value="deploy">
                    <Accordion.Control>
                      How to run your configuration
                    </Accordion.Control>
                    <Accordion.Panel>
                      <Stack gap="sm">
                        <Text size="sm">
                          Download core.yaml and configure its referenced
                          secrets on your Hookway host. Then validate and start
                          the server:
                        </Text>
                        <Code block>
                          {
                            "hookway validate --config core.yaml\nhookway serve --config core.yaml"
                          }
                        </Code>
                        <Text size="sm">
                          Make the public MCP URL reachable and allow this
                          browser’s origin in your deployment.
                        </Text>
                        <Button
                          variant="default"
                          onClick={download}
                          disabled={!complete}
                          style={{ alignSelf: "flex-start" }}
                        >
                          Download core.yaml
                        </Button>
                      </Stack>
                    </Accordion.Panel>
                  </Accordion.Item>
                </Accordion>
                {complete && step === 4 && (
                  <MCPTester
                    key={String(settings.endpoint.public_url)}
                    endpoint={String(settings.endpoint.public_url || "")}
                    anonymous={false}
                  />
                )}
              </Stack>
            </Box>
          </Box>
          <Group justify="space-between" className={classes.footer}>
            <Button
              variant="subtle"
              onClick={() => setStep((s) => Math.max(0, s - 1))}
              disabled={step === 0 || busy}
            >
              Back
            </Button>
            <Group gap="sm">
              {step === 0 && (
                <Button
                  disabled={
                    busy || !source.trim() || sourceBytes > MAX_OPENAPI_BYTES
                  }
                  onClick={() => void inspect()}
                  rightSection={<FiArrowRight aria-hidden />}
                >
                  Find operations
                </Button>
              )}
              {step === 1 && (
                <Button
                  disabled={!selected.length || badNames || busy}
                  onClick={() => {
                    setConfigured(true);
                    setStep(2);
                  }}
                  rightSection={<FiArrowRight aria-hidden />}
                >
                  Configure access
                </Button>
              )}
              {step === 2 && (
                <Button
                  disabled={
                    !selected.length || badNames || invalidDrafts || busy
                  }
                  onClick={() => {
                    if (accessPanel === "api") advanceAccess("callers");
                    else if (accessPanel === "callers")
                      advanceAccess("permissions");
                    else void generate();
                  }}
                  rightSection={<FiArrowRight aria-hidden />}
                >
                  {accessPanel === "api"
                    ? "Set agent access"
                    : accessPanel === "callers"
                      ? "Set tool permissions"
                      : "Review configuration"}
                </Button>
              )}
              {step === 3 && complete && (
                <>
                  <Button
                    onClick={download}
                    leftSection={<FiFileText aria-hidden />}
                  >
                    Download core.yaml
                  </Button>
                </>
              )}
              {step === 3 && !complete && (
                <Button
                  onClick={() => {
                    setStep(2);
                    setAccessPanel("permissions");
                  }}
                >
                  Edit configuration
                </Button>
              )}
              {step === 4 && <Button onClick={close}>Done</Button>}
            </Group>
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
          <Button variant="default" onClick={() => setExporting(true)}>
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
      {exporting && <ExportModal opened onClose={() => setExporting(false)} />}
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
      {requirements
        ? `API auth: ${requirements}`
        : "No API authentication declared"}
    </Text>
  );
}

function StepHeading({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <Box>
      <Title order={2} size="h3" tabIndex={-1} className={classes.heading}>
        {title}
      </Title>
      <Text size="sm" c="dimmed" mt="xs" maw="68ch">
        {description}
      </Text>
    </Box>
  );
}
function Method({ method }: { method: string }) {
  return (
    <Badge
      variant="light"
      color={method === "GET" ? "brand" : "gray"}
      radius="sm"
      className={classes.method}
    >
      {method}
    </Badge>
  );
}
function grantSummary(access: Record<string, unknown>) {
  const keys = Array.isArray(access.api_keys) ? access.api_keys.join(", ") : "";
  const oauth = Object.entries(access)
    .filter(
      ([key, value]) =>
        !["api_keys", "allow_mutation", "anonymous"].includes(key) &&
        value &&
        (!Array.isArray(value) || value.length),
    )
    .map(
      ([key, value]) =>
        `${key.replaceAll("_", " ")}: ${Array.isArray(value) ? value.join(", ") : String(value)}`,
    )
    .join("; ");
  return [keys, oauth].filter(Boolean).join(" · ") || "None";
}
