import { Code, List, Modal, Stack, Tabs, Text } from "@mantine/core";
import type { GraphDocument } from "./model";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";
import { toYaml } from "./toYaml";
import { toReplayYaml } from "./replayYaml";
import { scanEnvVars } from "./envScan";

export function EnvPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
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

  let coreVars: string[] = [];
  let replayVars: string[] = [];
  try {
    const doc: GraphDocument = {
      nodes, edges, serverConfig, coreReplayConfig, replayServiceConfig, configExtra, connections, destinations,
      activeFlow, otherFlows, deliveryFailurePolicy, flowExtra,
    };
    coreVars = scanEnvVars(toYaml(toConfig(doc).config));
    if (replayServiceConfig) replayVars = scanEnvVars(toReplayYaml(replayServiceConfig));
  } catch {
    // Export surfaces explain serialization failures; this inventory remains best effort.
  }
  const allVars = [...new Set([...coreVars, ...replayVars])].sort();

  return (
    <Modal opened={opened} onClose={onClose} title="Environment variables" size="lg" closeButtonProps={{ "aria-label": "Close Environment variables" }}>
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Set these before running each service. References use <Code>${"{VAR}"}</Code>; the builder never resolves their values.
        </Text>
        <Tabs defaultValue="all" keepMounted={false}>
          <Tabs.List>
            <Tabs.Tab value="all">All ({allVars.length})</Tabs.Tab>
            <Tabs.Tab value="core">Core ({coreVars.length})</Tabs.Tab>
            <Tabs.Tab value="replay">Replay ({replayVars.length})</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value="all" pt="sm"><VariableList values={allVars} /></Tabs.Panel>
          <Tabs.Panel value="core" pt="sm"><VariableList values={coreVars} /></Tabs.Panel>
          <Tabs.Panel value="replay" pt="sm"><VariableList values={replayVars} absent={!replayServiceConfig} /></Tabs.Panel>
        </Tabs>
      </Stack>
    </Modal>
  );
}

function VariableList({ values, absent = false }: { values: string[]; absent?: boolean }) {
  if (absent) return <Text size="sm" c="dimmed">No replay.yaml is present.</Text>;
  if (values.length === 0) return <Text size="sm" c="dimmed">No environment variables referenced.</Text>;
  return (
    <List size="sm">
      {values.map((value) => <List.Item key={value}><Code>{value}</Code></List.Item>)}
    </List>
  );
}
