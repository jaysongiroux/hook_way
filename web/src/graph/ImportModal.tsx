import { useState } from "react";
import { Modal, Textarea, Button, Group, Alert, Stack, Tabs, Text } from "@mantine/core";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";
import { fromReplayYaml } from "./replayYaml";

export function ImportModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const [active, setActive] = useState<string | null>("core");
  const [coreText, setCoreText] = useState("");
  const [replayText, setReplayText] = useState("");
  const [errors, setErrors] = useState({ core: "", replay: "" });

  const doImport = () => {
    try {
      const state = useGraphStore.getState();
      if (active === "replay") {
        state.setReplayServiceConfig(fromReplayYaml(replayText));
        setErrors((current) => ({ ...current, replay: "" }));
      } else {
        const doc = fromYaml(coreText);
        state.setGraph({ ...doc, replayServiceConfig: state.replayServiceConfig });
        setErrors((current) => ({ ...current, core: "" }));
      }
      onClose();
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      setErrors((current) => ({ ...current, [active === "replay" ? "replay" : "core"]: message }));
    }
  };

  const isReplay = active === "replay";
  const error = isReplay ? errors.replay : errors.core;

  return (
    <Modal opened={opened} onClose={onClose} title="Import configs" size="lg">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">Replace one YAML document without disturbing the other. Semantic issues can be repaired after import.</Text>
        <Tabs value={active} onChange={setActive} keepMounted={false}>
          <Tabs.List>
            <Tabs.Tab value="core">Core YAML</Tabs.Tab>
            <Tabs.Tab value="replay">Replay YAML</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="core" pt="sm">
            {errors.core && <Alert color="red" title="Could not parse core YAML" mb="sm">{errors.core}</Alert>}
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
            {errors.replay && <Alert color="red" title="Could not parse replay YAML" mb="sm">{errors.replay}</Alert>}
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
        </Tabs>
        {error && <Text size="xs" c="red">Fix the YAML above and try again.</Text>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button onClick={doImport}>Import {isReplay ? "replay" : "core"} YAML</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
