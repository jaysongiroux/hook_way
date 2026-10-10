import {
  entryCandidates,
  fromBundle,
  missingFlowFiles,
  readYAMLFiles,
} from "./bundle";
import { useMemo, useRef, useState } from "react";
import {
  Modal,
  Textarea,
  Button,
  Group,
  Alert,
  Stack,
  Tabs,
  Text,
  Select,
} from "@mantine/core";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";
import { fromReplayYaml } from "./replayYaml";

export function ImportModal({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const [active, setActive] = useState<string | null>("core");
  const [coreText, setCoreText] = useState("");
  const [replayText, setReplayText] = useState("");
  const [errors, setErrors] = useState({ core: "", replay: "" });

  const [files, setFiles] = useState<Record<string, string>>({});
  const [entry, setEntry] = useState("");
  const [replayFile, setReplayFile] = useState("__auto__");
  const [fileError, setFileError] = useState("");
  const [reading, setReading] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const directoryInput = useRef<HTMLInputElement>(null);
  const candidates = useMemo(() => entryCandidates(files), [files]);
  const missing = useMemo(() => {
    try {
      return entry ? missingFlowFiles(entry, files) : [];
    } catch {
      return [];
    }
  }, [entry, files]);
  const readFiles = async (selected: File[]) => {
    setReading(true);
    setFileError("");
    try {
      const loaded = await readYAMLFiles(selected);
      const entries = entryCandidates(loaded);
      setFiles(loaded);
      setEntry(entries.length === 1 ? entries[0] : "");
    } catch (error) {
      setFiles({});
      setEntry("");
      setFileError(String(error));
    } finally {
      setReading(false);
    }
  };

  const doImport = () => {
    try {
      const state = useGraphStore.getState();
      if (active === "files") {
        if (!entry) throw new Error("Select the entry YAML before importing.");
        const doc = fromBundle(
          { entry, files },
          replayFile === "__auto__"
            ? undefined
            : replayFile === "__none__"
              ? null
              : replayFile,
        );
        state.setGraph(doc);
        setFileError("");
      } else if (active === "replay") {
        state.setReplayServiceConfig(fromReplayYaml(replayText));
        setErrors((current) => ({ ...current, replay: "" }));
      } else {
        const doc = fromYaml(coreText);
        state.setGraph({
          ...doc,
          replayServiceConfig: state.replayServiceConfig,
          replayFilename: state.replayFilename,
        });
        setErrors((current) => ({ ...current, core: "" }));
      }
      onClose();
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      if (active === "files") setFileError(message);
      else
        setErrors((current) => ({
          ...current,
          [active === "replay" ? "replay" : "core"]: message,
        }));
    }
  };

  const isReplay = active === "replay";
  const error =
    active === "files" ? fileError : isReplay ? errors.replay : errors.core;

  return (
    <Modal opened={opened} onClose={onClose} title="Import configs" size="lg">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Paste a document, or reopen a ZIP or set of YAML files. File imports
          replace the complete workspace after every document parses. Semantic
          issues can be repaired after import.
        </Text>
        <Tabs value={active} onChange={setActive} keepMounted={false}>
          <Tabs.List>
            <Tabs.Tab value="core">Core YAML</Tabs.Tab>
            <Tabs.Tab value="replay">Replay YAML</Tabs.Tab>
            <Tabs.Tab value="files">Files / ZIP</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="core" pt="sm">
            {errors.core && (
              <Alert color="red" title="Could not parse core YAML" mb="sm">
                {errors.core}
              </Alert>
            )}
            <Textarea
              label="Core YAML"
              placeholder="Paste core.yaml here"
              value={coreText}
              onChange={(e) => setCoreText(e.currentTarget.value)}
              autosize
              minRows={12}
              styles={{ input: { fontFamily: "ui-monospace, monospace" } }}
            />
          </Tabs.Panel>
          <Tabs.Panel value="replay" pt="sm">
            {errors.replay && (
              <Alert color="red" title="Could not parse replay YAML" mb="sm">
                {errors.replay}
              </Alert>
            )}
            <Textarea
              label="Replay YAML"
              placeholder="Paste replay.yaml here"
              value={replayText}
              onChange={(e) => setReplayText(e.currentTarget.value)}
              autosize
              minRows={12}
              styles={{ input: { fontFamily: "ui-monospace, monospace" } }}
            />
          </Tabs.Panel>
          <Tabs.Panel value="files" pt="sm">
            <Stack gap="sm">
              <Text size="sm" c="dimmed">
                Select a ZIP, the entry YAML and its flow files, or their
                directory. Limits: 1,024 files, 5 MiB per YAML, 64 MiB total.
              </Text>
              <Group gap="sm">
                <Button
                  variant="default"
                  disabled={reading}
                  onClick={() => fileInput.current?.click()}
                >
                  Choose YAML files or ZIP
                </Button>
                <Button
                  variant="default"
                  disabled={reading}
                  onClick={() => directoryInput.current?.click()}
                >
                  Choose directory
                </Button>
              </Group>
              <input
                ref={fileInput}
                aria-label="YAML files or ZIP"
                type="file"
                multiple
                accept=".yaml,.yml,.zip"
                hidden
                disabled={reading}
                onChange={(event) => {
                  void readFiles(Array.from(event.currentTarget.files ?? []));
                  event.currentTarget.value = "";
                }}
              />
              <input
                ref={directoryInput}
                aria-label="Configuration directory"
                type="file"
                multiple
                {...{ webkitdirectory: "" }}
                hidden
                disabled={reading}
                onChange={(event) => {
                  void readFiles(Array.from(event.currentTarget.files ?? []));
                  event.currentTarget.value = "";
                }}
              />
              {Object.keys(files).length > 0 && (
                <>
                  <Text size="sm">
                    {Object.keys(files).length} YAML files selected
                  </Text>
                  <Select
                    label="Entry YAML"
                    description={
                      candidates.length > 1
                        ? "Multiple entry files found. Choose the configuration to open."
                        : "The file containing central settings and flow definitions or references."
                    }
                    data={candidates.length ? candidates : Object.keys(files)}
                    value={entry || null}
                    onChange={(value) => setEntry(value ?? "")}
                  />
                  <Select
                    label="Replay YAML"
                    data={[
                      { value: "__auto__", label: "Detect automatically" },
                      { value: "__none__", label: "No replay configuration" },
                      ...Object.keys(files)
                        .filter((name) => name !== entry)
                        .map((name) => ({ value: name, label: name })),
                    ]}
                    value={replayFile}
                    onChange={(value) => setReplayFile(value ?? "__auto__")}
                  />
                  {missing.map((filename) => (
                    <Stack key={filename} gap={4}>
                      <Text size="sm" c="red">
                        Missing flow file: {filename}
                      </Text>
                      <Select
                        label={`Attach file to ${filename}`}
                        data={Object.keys(files).filter(
                          (name) => name !== entry,
                        )}
                        onChange={(selected) => {
                          if (selected)
                            setFiles((current) => ({
                              ...current,
                              [filename]: current[selected],
                            }));
                        }}
                      />
                    </Stack>
                  ))}
                </>
              )}
              {fileError && (
                <Alert color="red" title="Could not import files">
                  {fileError}
                </Alert>
              )}
            </Stack>
          </Tabs.Panel>
        </Tabs>
        {error && (
          <Text size="xs" c="red">
            {active === "files"
              ? "Choose corrected files and try again."
              : "Fix the YAML above and try again."}
          </Text>
        )}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={doImport}
            disabled={
              reading || (active === "files" && (!entry || missing.length > 0))
            }
          >
            {active === "files"
              ? "Import workspace"
              : `Import ${isReplay ? "replay" : "core"} YAML`}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
