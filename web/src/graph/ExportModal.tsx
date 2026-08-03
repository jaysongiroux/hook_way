import { Modal, Code, Group, Button, Alert, ScrollArea } from "@mantine/core";
import { useGraphStore } from "./store";
import { toConfig, ExportError } from "./toConfig";
import { toYaml } from "./toYaml";

export function ExportModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);

  let yaml = "";
  let error = "";
  try {
    const { config, notes } = toConfig(nodes, edges);
    yaml = toYaml(config, notes);
  } catch (e) {
    error = e instanceof ExportError ? e.message : String(e);
  }

  const download = () => {
    const blob = new Blob([yaml], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "hookway.yaml";
    // Firefox requires the anchor to be in the document for a synthetic click,
    // and revocation must be deferred so it doesn't race the download.
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  const copy = () => {
    navigator.clipboard?.writeText(yaml).catch(() => {
      /* clipboard unavailable (insecure context / denied) — no-op */
    });
  };

  return (
    <Modal opened={opened} onClose={onClose} title="Export config" size="lg">
      {error ? (
        <Alert color="red" title="Cannot export">{error}</Alert>
      ) : (
        <>
          <ScrollArea h={400}>
            <Code block>{yaml}</Code>
          </ScrollArea>
          <Group mt="md" justify="flex-end">
            <Button variant="default" onClick={copy}>Copy</Button>
            <Button onClick={download}>Download hookway.yaml</Button>
          </Group>
        </>
      )}
    </Modal>
  );
}
