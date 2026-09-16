import { Drawer, Stack, Text, Code, List } from "@mantine/core";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";
import { toYaml } from "./toYaml";
import { scanEnvVars } from "./envScan";
import type { GraphDocument } from "./model";

export function EnvPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const configExtra = useGraphStore((s) => s.configExtra);
  const destinations = useGraphStore((s) => s.destinations);
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const otherFlows = useGraphStore((s) => s.otherFlows);
  const deliveryFailurePolicy = useGraphStore((s) => s.deliveryFailurePolicy);
  let vars: string[] = [];
  try {
    const doc: GraphDocument = {
      nodes,
      edges,
      serverConfig,
      configExtra,
      destinations,
      activeFlow,
      otherFlows,
      deliveryFailurePolicy,
    };
    const { config } = toConfig(doc);
    vars = scanEnvVars(toYaml(config));
  } catch {
    vars = [];
  }
  return (
    <Drawer opened={opened} onClose={onClose} position="right" title="Environment variables">
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Set these before running Hookway. Referenced via <Code>${"{VAR}"}</Code> in the config.
        </Text>
        {vars.length === 0 ? (
          <Text size="sm" c="dimmed">
            No environment variables referenced.
          </Text>
        ) : (
          <List size="sm">
            {vars.map((v) => (
              <List.Item key={v}>
                <Code>{v}</Code>
              </List.Item>
            ))}
          </List>
        )}
      </Stack>
    </Drawer>
  );
}
