import { useState } from "react";
import { Modal, Textarea, Button, Group, Alert } from "@mantine/core";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";

export function ImportModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const setGraph = useGraphStore((s) => s.setGraph);

  const doImport = () => {
    try {
      const doc = fromYaml(text);
      setGraph(doc);
      setError("");
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title="Import config" size="lg">
      {error && <Alert color="red" title="Could not parse" mb="sm">{error}</Alert>}
      <Textarea
        label="Config YAML"
        placeholder="Paste your hookway.yaml here"
        value={text}
        onChange={(e) => setText(e.currentTarget.value)}
        autosize
        minRows={12}
        styles={{ input: { fontFamily: "ui-monospace, monospace" } }}
      />
      <Group mt="md" justify="flex-end">
        <Button variant="default" onClick={onClose}>Cancel</Button>
        <Button onClick={doImport}>Import</Button>
      </Group>
    </Modal>
  );
}
