import { Drawer, Stack, Text, Code, List } from "@mantine/core";
import { useGraphStore } from "./store";
import { toConfig } from "./toConfig";
import { toYaml } from "./toYaml";
import { scanEnvVars } from "./envScan";
import { BUILDER_FLOATING_CLASS, BUILDER_FLOATING_HEADER_CLASS, BUILDER_OVERLAY_CLASS } from "../builderStyles";

export function EnvPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const nodes = useGraphStore((s) => s.nodes);
  const edges = useGraphStore((s) => s.edges);
  const serverConfig = useGraphStore((s) => s.serverConfig);
  const configExtra = useGraphStore((s) => s.configExtra);
  let vars: string[] = [];
  try {
    const { config, notes } = toConfig(nodes, edges, serverConfig, configExtra);
    vars = scanEnvVars(toYaml(config, notes));
  } catch {
    vars = [];
  }
  return (
    <Drawer
      opened={opened}
      onClose={onClose}
      position="right"
      title="Environment variables"
      classNames={{ content: BUILDER_FLOATING_CLASS, header: BUILDER_FLOATING_HEADER_CLASS }}
      overlayProps={{ className: BUILDER_OVERLAY_CLASS }}
    >
      <Stack gap="sm">
        <Text size="sm" c="dimmed">
          Set these before running Hookway. Referenced via <Code>${"{VAR}"}</Code> in the config.
        </Text>
        {vars.length === 0 ? (
          <Text size="sm" c="dimmed">No environment variables referenced.</Text>
        ) : (
          <List size="sm">{vars.map((v) => <List.Item key={v}><Code>{v}</Code></List.Item>)}</List>
        )}
      </Stack>
    </Drawer>
  );
}
