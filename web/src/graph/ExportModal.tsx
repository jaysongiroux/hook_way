import { Select, TextInput } from "@mantine/core";
import {
  toBundle,
  flowFilenames,
  needsZip,
  zipBundle,
  yamlPath,
} from "./bundle";
import { IssueAlert } from "../shared/ui/IssueAlert";
import { LoadingState } from "../shared/ui/States";
import {
  Alert,
  Button,
  Code,
  Group,
  Modal,
  ScrollArea,
  Stack,
  Tabs,
  Text,
} from "@mantine/core";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useGraphStore } from "./store";
import { useBuilderValidation } from "./BuilderValidation";
import {
  validateReplayYaml,
  validateYaml,
  validateBundle,
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
  const state = useGraphStore();
  const { draftIssues } = useBuilderValidation();
  const [selected, setSelected] = useState<string>("entry");
  const [activeTab, setActiveTab] = useState<string>("core");
  const doc = useMemo(() => state.document(), [state]);
  const result = useMemo(() => {
    try {
      return { bundle: toBundle(doc), error: "" };
    } catch (error) {
      return { bundle: null, error: String(error) };
    }
  }, [doc]);
  const flowFiles = flowFilenames(doc);
  const flowNames = Object.keys(flowFiles).sort();
  const selectedFlow =
    doc.yamlMode === "split" && selected.startsWith("flow:")
      ? selected.slice(5)
      : null;
  const previewPath = (value: string) => {
    try {
      return yamlPath(value);
    } catch {
      return value;
    }
  };
  const entry = previewPath(doc.entryFilename ?? "core.yaml");
  const entryDirectory = entry.includes("/")
    ? entry.slice(0, entry.lastIndexOf("/") + 1)
    : "";
  const filename =
    selectedFlow && flowFiles[selectedFlow]
      ? entryDirectory + previewPath(flowFiles[selectedFlow])
      : entry;
  const bundle = result.bundle;
  const isZIP = bundle ? needsZip(doc, bundle) : false;
  const [downloadError, setDownloadError] = useState("");
  const validateWorkspace = useCallback(async () => {
    if (!bundle) return null;
    const core =
      doc.yamlMode === "split"
        ? await validateBundle(bundle)
        : await validateYaml(bundle.files[bundle.entry]);
    if (core === null) return null;
    const issues = core.map((issue) => ({
      ...issue,
      source:
        doc.yamlMode === "split"
          ? (issue.source ?? bundle.entry)
          : bundle.entry,
    }));
    if (doc.replayServiceConfig !== null) {
      const replayName = previewPath(doc.replayFilename ?? "replay.yaml");
      const replay = await validateReplayYaml(bundle.files[replayName]);
      if (replay === null) return null;
      issues.push(...replay.map((issue) => ({ ...issue, source: replayName })));
    }
    return issues;
  }, [bundle, doc.yamlMode, doc.replayServiceConfig, doc.replayFilename]);
  const downloadWorkspace = async () => {
    if (!bundle) return;
    setDownloadError("");
    try {
      const bytes = isZIP
        ? await zipBundle(bundle)
        : new TextEncoder().encode(bundle.files[bundle.entry]);
      const blob = new Blob([new Uint8Array(bytes)], {
        type: isZIP ? "application/zip" : "application/yaml",
      });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = isZIP
        ? "hookway-config.zip"
        : bundle.entry.split("/").pop()!;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (error) {
      setDownloadError(String(error));
    }
  };
  const showSource = (source: string) => {
    if (source === previewPath(doc.replayFilename ?? "replay.yaml")) {
      setActiveTab("replay");
      return;
    }
    setActiveTab("core");
    if (source === entry) setSelected("entry");
    else {
      const name = flowNames.find(
        (name) => entryDirectory + previewPath(flowFiles[name]) === source,
      );
      if (name) setSelected(`flow:${name}`);
    }
  };
  return (
    <Modal opened={opened} onClose={onClose} title="Export configs" size="lg">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Choose how flows are stored. Downloads include every configured YAML
          file, including replay.
        </Text>
        <Select
          label="YAML organization"
          value={doc.yamlMode ?? "single"}
          allowDeselect={false}
          data={[
            { value: "single", label: "Single YAML" },
            { value: "split", label: "One YAML per flow" },
          ]}
          onChange={(value) =>
            state.setYAMLMode(value === "split" ? "split" : "single")
          }
        />
        <TextInput
          label="Entry YAML filename"
          description="Paths are relative to the downloaded configuration directory."
          value={doc.entryFilename ?? "core.yaml"}
          onChange={(event) =>
            state.setEntryFilename(event.currentTarget.value)
          }
        />
        <Tabs
          value={activeTab}
          onChange={(value) => setActiveTab(value ?? "core")}
          keepMounted={false}
        >
          <Tabs.List>
            <Tabs.Tab value="core">Core YAML</Tabs.Tab>
            <Tabs.Tab value="replay">Replay YAML</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="core" pt="sm">
            <Stack gap="sm">
              {doc.yamlMode === "split" && (
                <>
                  <ScrollArea.Autosize mah={160}>
                    <Stack gap={4}>
                      <Button
                        variant={selected === "entry" ? "light" : "subtle"}
                        justify="flex-start"
                        aria-pressed={selected === "entry"}
                        styles={{
                          label: {
                            whiteSpace: "normal",
                            overflowWrap: "anywhere",
                            textAlign: "left",
                          },
                          root: {
                            height: "auto",
                            minHeight: 36,
                            paddingBlock: 8,
                          },
                        }}
                        onClick={() => setSelected("entry")}
                      >
                        {entry}
                      </Button>
                      {flowNames.map((name) => (
                        <Button
                          key={name}
                          variant={
                            selected === `flow:${name}` ? "light" : "subtle"
                          }
                          justify="flex-start"
                          aria-pressed={selected === `flow:${name}`}
                          styles={{
                            label: {
                              whiteSpace: "normal",
                              overflowWrap: "anywhere",
                              textAlign: "left",
                            },
                            root: {
                              height: "auto",
                              minHeight: 36,
                              paddingBlock: 8,
                            },
                          }}
                          onClick={() => setSelected(`flow:${name}`)}
                        >
                          {flowFiles[name]} — {name}
                        </Button>
                      ))}
                    </Stack>
                  </ScrollArea.Autosize>
                  {selectedFlow && flowFiles[selectedFlow] !== undefined && (
                    <TextInput
                      label="Flow YAML filename"
                      description="Relative to the entry YAML directory. Renaming the flow keeps this filename."
                      value={flowFiles[selectedFlow]}
                      onChange={(event) =>
                        state.setFlowFilename(
                          selectedFlow,
                          event.currentTarget.value,
                        )
                      }
                    />
                  )}
                </>
              )}
              <DocumentExport
                opened={opened}
                yaml={bundle?.files[filename] ?? ""}
                error={result.error}
                filename={filename}
                command={`hookway validate --config ${entry}`}
                validate={validateWorkspace}
                draftIssues={draftIssues}
                onIssue={onIssue}
                onSource={showSource}
                onDownload={downloadWorkspace}
                downloadFilename={isZIP ? "hookway-config.zip" : entry}
              />
            </Stack>
          </Tabs.Panel>
          <Tabs.Panel value="replay" pt="sm">
            {doc.replayServiceConfig !== null ? (
              <Stack gap="sm">
                <TextInput
                  label="Replay YAML filename"
                  value={doc.replayFilename ?? "replay.yaml"}
                  onChange={(event) =>
                    state.setReplayFilename(event.currentTarget.value)
                  }
                />
                <DocumentExport
                  opened={opened}
                  yaml={
                    bundle?.files[
                      previewPath(doc.replayFilename ?? "replay.yaml")
                    ] ?? ""
                  }
                  error={result.error}
                  filename={doc.replayFilename ?? "replay.yaml"}
                  command="hookway-replay validate"
                  validate={validateWorkspace}
                  draftIssues={draftIssues}
                  onIssue={onIssue}
                  onSource={showSource}
                  onDownload={downloadWorkspace}
                  downloadFilename="hookway-config.zip"
                />
              </Stack>
            ) : (
              <Stack align="flex-start" gap="sm" py="lg">
                <Text fw={600}>No replay.yaml in this workspace</Text>
                <Text size="sm" c="dimmed">
                  Replay stays optional until you add or import its
                  configuration.
                </Text>
                <Button size="sm" onClick={state.addReplayConfiguration}>
                  Add replay configuration
                </Button>
              </Stack>
            )}
          </Tabs.Panel>
        </Tabs>
        {downloadError && (
          <Alert color="red" title="Download failed">
            {downloadError}
          </Alert>
        )}
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
  onSource,
  onDownload,
  downloadFilename,
}: {
  onSource?: (source: string) => void;
  onDownload: () => Promise<void>;
  downloadFilename: string;
  draftIssues?: { key: string; error: string }[];
  onIssue?: (field: string) => void;
  opened: boolean;
  yaml: string;
  error: string;
  filename: string;
  command: string;
  validate: (yaml: string) => Promise<ValidationIssue[] | null>;
}) {
  const [downloading, setDownloading] = useState(false);
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
        <LoadingState>Checking configuration…</LoadingState>
      )}
      {validationUnavailable && (
        <Alert
          color="red"
          title="Validation unavailable"
          data-testid="validation-unavailable"
        >
          Validation could not check this document. Retry local validation in
          the builder. Run{" "}
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
          action={
            onIssue
              ? (issue) =>
                  issue.field.startsWith("flows.") &&
                  issue.field.includes(".nodes")
                    ? {
                        label: "Open affected node",
                        onClick: () => onIssue(issue.field),
                      }
                    : undefined
              : undefined
          }
        />
      )}
      {issues && blocking.length === 0 && warnings.length > 0 && (
        <IssueAlert
          color="yellow"
          title="Valid with warnings"
          issues={warnings}
          testId="validation-warnings"
          action={
            onIssue
              ? (issue) =>
                  issue.field.startsWith("flows.") &&
                  issue.field.includes(".nodes")
                    ? {
                        label: "Open affected node",
                        onClick: () => onIssue(issue.field),
                      }
                    : undefined
              : undefined
          }
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
      {onSource && (
        <Group gap="xs">
          {[
            ...new Set(
              (issues ?? [])
                .filter((issue) => issue.source && issue.source !== filename)
                .map((issue) => issue.source!),
            ),
          ].map((source) => (
            <Button
              key={source}
              size="xs"
              variant="subtle"
              onClick={() => onSource(source)}
            >
              Show {source}
            </Button>
          ))}
        </Group>
      )}
      <ScrollArea h={300}>
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
          loading={downloading}
          onClick={async () => {
            setDownloading(true);
            try {
              await onDownload();
            } finally {
              setDownloading(false);
            }
          }}
        >
          Download {downloadFilename}
        </Button>
      </Group>
    </Stack>
  );
}
