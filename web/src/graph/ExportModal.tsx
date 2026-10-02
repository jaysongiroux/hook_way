import {
  Alert,
  Button,
  Code,
  Group,
  List,
  Modal,
  ScrollArea,
  Stack,
  Tabs,
  Text,
} from "@mantine/core";
import { useEffect, useMemo, useState } from "react";
import type { GraphDocument } from "./model";
import { useGraphStore } from "./store";
import { toConfig, ExportError } from "./toConfig";
import { toYaml } from "./toYaml";
import { toReplayYaml } from "./replayYaml";
import { useBuilderValidation } from "./BuilderValidation";
import {
  validateReplayYaml,
  validateYaml,
  type ValidationIssue,
} from "./validate";

export function ExportModal({
  opened,
  onClose,
  onIssue,
}: {
  opened: boolean;
  onClose: () => void;
  onIssue?: (field: string) => void;
}) {
  const { draftIssues } = useBuilderValidation();
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const coreReplayConfig = useGraphStore((s) => s.coreReplayConfig);
  const replayServiceConfig = useGraphStore((s) => s.replayServiceConfig);
  const configExtra = useGraphStore((s) => s.configExtra);
  const connections = useGraphStore((s) => s.connections);
  const destinations = useGraphStore((s) => s.destinations);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);
  const flowExtra = useGraphStore((s) => s.flowExtra);
  const addReplayConfiguration = useGraphStore((s) => s.addReplayConfiguration);

  const coreExport = useMemo(() => {
    const doc: GraphDocument = {
      nodes,
      edges,
      serverConfig,
      coreReplayConfig,
      replayServiceConfig,
      configExtra,
      connections,
      destinations,
      activeFlow,
      otherFlows,
      deliveryFailurePolicy,
      flowExtra,
    };
    try {
      return { yaml: toYaml(toConfig(doc).config), error: "" };
    } catch (error) {
      return {
        yaml: "",
        error: error instanceof ExportError ? error.message : String(error),
      };
    }
  }, [
    nodes,
    edges,
    serverConfig,
    coreReplayConfig,
    replayServiceConfig,
    configExtra,
    connections,
    destinations,
    activeFlow,
    otherFlows,
    deliveryFailurePolicy,
    flowExtra,
  ]);

  const replayExport = useMemo(() => {
    if (!replayServiceConfig) return { yaml: "", error: "" };
    try {
      return { yaml: toReplayYaml(replayServiceConfig), error: "" };
    } catch (error) {
      return { yaml: "", error: String(error) };
    }
  }, [replayServiceConfig]);

  return (
    <Modal opened={opened} onClose={onClose} title="Export configs" size="lg">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Preview and validate each deployment document independently. The
          builder does not connect to a running Hookway instance.
        </Text>
        <Tabs defaultValue="core" keepMounted={false}>
          <Tabs.List>
            <Tabs.Tab value="core">Core YAML</Tabs.Tab>
            <Tabs.Tab value="replay">Replay YAML</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="core" pt="sm">
            <DocumentExport
              opened={opened}
              yaml={coreExport.yaml}
              error={coreExport.error}
              filename="core.yaml"
              draftIssues={draftIssues.filter(
                (d) => !d.key.startsWith("replay/"),
              )}
              onIssue={onIssue}
              command="hookway validate"
              validate={validateYaml}
            />
          </Tabs.Panel>
          <Tabs.Panel value="replay" pt="sm">
            {replayServiceConfig ? (
              <DocumentExport
                opened={opened}
                yaml={replayExport.yaml}
                error={replayExport.error}
                filename="replay.yaml"
                draftIssues={draftIssues.filter((d) =>
                  d.key.startsWith("replay/"),
                )}
                command="hookway-replay validate"
                validate={validateReplayYaml}
              />
            ) : (
              <Stack align="flex-start" gap="sm" py="lg">
                <Text fw={600}>No replay.yaml in this workspace</Text>
                <Text size="sm" c="dimmed">
                  Replay stays optional until you add or import its
                  configuration.
                </Text>
                <Button size="sm" onClick={addReplayConfiguration}>
                  Add replay configuration
                </Button>
              </Stack>
            )}
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Modal>
  );
}

function DocumentExport({
  opened,
  yaml,
  error,
  filename,
  command,
  validate,
  draftIssues = [],
  onIssue,
}: {
  draftIssues?: { key: string; error: string }[];
  onIssue?: (field: string) => void;
  opened: boolean;
  yaml: string;
  error: string;
  filename: string;
  command: string;
  validate: (yaml: string) => Promise<ValidationIssue[] | null>;
}) {
  const [issues, setIssues] = useState<ValidationIssue[] | null>(null);
  const [validationUnavailable, setValidationUnavailable] = useState(false);
  const [copyStatus, setCopyStatus] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  useEffect(() => setCopyStatus("idle"), [opened, yaml]);
  useEffect(() => {
    if (!opened || error || !yaml) {
      setIssues(null);
      setValidationUnavailable(false);
      return;
    }
    let cancelled = false;
    setIssues(null);
    setValidationUnavailable(false);
    void validate(yaml)
      .then((result) => {
        if (!cancelled) {
          setIssues(result);
          setValidationUnavailable(result === null);
        }
      })
      .catch(() => {
        if (!cancelled) setValidationUnavailable(true);
      });
    return () => {
      cancelled = true;
    };
  }, [opened, yaml, error, validate]);

  const blocking = (issues ?? []).filter(
    (issue) => issue.severity !== "warning",
  );
  const warnings = (issues ?? []).filter(
    (issue) => issue.severity === "warning",
  );
  const download = () => {
    const blob = new Blob([yaml], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };
  const copy = async () => {
    try {
      if (!navigator.clipboard) throw new Error("Clipboard unavailable");
      await navigator.clipboard.writeText(yaml);
      setCopyStatus("copied");
    } catch {
      setCopyStatus("failed");
    }
  };

  if (error)
    return (
      <Alert color="red" title="Cannot export">
        {error}
      </Alert>
    );
  return (
    <Stack gap="sm">
      {draftIssues.length > 0 && (
        <Alert color="red" title="Unresolved form drafts">
          <Text size="sm">
            Correct or revert these drafts before exporting. The preview
            contains the last valid values.
          </Text>
          {draftIssues.map((d) => (
            <Text key={d.key} size="sm">
              {d.key}: {d.error}
            </Text>
          ))}
        </Alert>
      )}
      {!issues && !validationUnavailable && (
        <Text size="sm" role="status">
          Checking configuration…
        </Text>
      )}
      {validationUnavailable && (
        <Alert
          color="red"
          title="Validation unavailable"
          data-testid="validation-unavailable"
        >
          The browser validator and editor API could not check this document.
          Run{" "}
          <Text span ff="monospace">
            {command}
          </Text>{" "}
          before using it.
        </Alert>
      )}
      {issues && blocking.length > 0 && (
        <IssueAlert
          color="red"
          title="Validation errors"
          issues={blocking}
          testId="validation-errors"
          onIssue={onIssue}
        />
      )}
      {issues && blocking.length === 0 && warnings.length > 0 && (
        <IssueAlert
          color="yellow"
          title="Valid with warnings"
          issues={warnings}
          testId="validation-warnings"
          onIssue={onIssue}
        />
      )}
      {issues && blocking.length === 0 && warnings.length === 0 && (
        <Alert color="green" title="Valid" data-testid="validation-ok">
          This document passes browser-safe{" "}
          <Text span ff="monospace">
            {command}
          </Text>{" "}
          checks.
        </Alert>
      )}
      <ScrollArea h={400}>
        <Code block data-testid={`export-${filename}`}>
          {yaml}
        </Code>
      </ScrollArea>
      {copyStatus !== "idle" && (
        <Text
          size="sm"
          role="status"
          c={copyStatus === "failed" ? "red" : "dimmed"}
        >
          {copyStatus === "copied"
            ? "Copied to clipboard."
            : "Could not copy to the clipboard. Download the YAML instead."}
        </Text>
      )}
      <Group justify="flex-end">
        <Button
          variant="default"
          disabled={
            draftIssues.length > 0 ||
            blocking.length > 0 ||
            (!issues && !validationUnavailable)
          }
          onClick={copy}
        >
          Copy
        </Button>
        <Button
          disabled={
            draftIssues.length > 0 ||
            blocking.length > 0 ||
            (!issues && !validationUnavailable)
          }
          onClick={download}
        >
          Download {filename}
        </Button>
      </Group>
    </Stack>
  );
}

function IssueAlert({
  color,
  title,
  issues,
  testId,
  onIssue,
}: {
  onIssue?: (field: string) => void;
  color: "red" | "yellow";
  title: string;
  issues: ValidationIssue[];
  testId: string;
}) {
  return (
    <Alert color={color} title={title} data-testid={testId}>
      <List size="sm">
        {issues.map((issue, index) => (
          <List.Item key={`${issue.field}-${index}`}>
            <Text span ff="monospace">
              {issue.field}
            </Text>
            : {issue.message}
            {issue.suggestion && <Text size="sm">{issue.suggestion}</Text>}
            {onIssue &&
              issue.field.startsWith("flows.") &&
              issue.field.includes(".nodes") && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  onClick={() => onIssue(issue.field)}
                >
                  Open affected node
                </Button>
              )}
          </List.Item>
        ))}
      </List>
    </Alert>
  );
}
