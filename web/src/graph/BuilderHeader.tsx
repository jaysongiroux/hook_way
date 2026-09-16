import { Box, Button, Group, Menu, Text } from "@mantine/core";
import { Logo } from "../brand/Logo";
import { HEADER_HEIGHT, HEADER_STYLE, TOOLBAR_DIVIDER } from "../brand/chrome";
import { DARK } from "../tokens";
import { FlowSwitcher } from "./FlowSwitcher";

export function BuilderHeader({
  paletteOpen,
  inspectorOpen,
  onTogglePalette,
  onToggleInspector,
  onImport,
  onExport,
  onDestinations,
  onEnv,
  onServer,
  onLoadExample,
  onClear,
}: {
  paletteOpen: boolean;
  inspectorOpen: boolean;
  onTogglePalette: () => void;
  onToggleInspector: () => void;
  onImport: () => void;
  onExport: () => void;
  onDestinations: () => void;
  onEnv: () => void;
  onServer: () => void;
  onLoadExample: () => void;
  onClear: () => void;
}) {
  return (
    <Box component="header" px="md" style={{ ...HEADER_STYLE, flexShrink: 0 }}>
      <Group mih={HEADER_HEIGHT} justify="space-between" align="center" wrap="wrap" gap="xs" style={{ width: "100%" }}>
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <Logo to="/" />
          <Text size="sm" c={DARK.muted} fw={500} visibleFrom="sm">
            Builder
          </Text>
          <Box aria-hidden style={TOOLBAR_DIVIDER} />
          <FlowSwitcher />
        </Group>
        <Group gap="xs" wrap="nowrap">
          <Button.Group>
            <Button
              size="xs"
              variant={paletteOpen ? "filled" : "default"}
              aria-pressed={paletteOpen}
              onClick={onTogglePalette}
            >
              Nodes
            </Button>
            <Button
              size="xs"
              variant={inspectorOpen ? "filled" : "default"}
              aria-pressed={inspectorOpen}
              onClick={onToggleInspector}
            >
              Inspector
            </Button>
          </Button.Group>
          <Box aria-hidden style={TOOLBAR_DIVIDER} />
          <Menu position="bottom-end" shadow="md">
            <Menu.Target>
              <Button size="xs" variant="default">
                Config
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Label>YAML</Menu.Label>
              <Menu.Item onClick={onImport}>Import YAML</Menu.Item>
              <Menu.Item onClick={onExport}>Export YAML</Menu.Item>
              <Menu.Divider />
              <Menu.Item onClick={onLoadExample}>Load production example</Menu.Item>
              <Menu.Divider />
              <Menu.Label>Workspace</Menu.Label>
              <Menu.Item onClick={onDestinations}>Destinations</Menu.Item>
              <Menu.Item onClick={onEnv}>Environment</Menu.Item>
              <Menu.Item onClick={onServer}>Server</Menu.Item>
            </Menu.Dropdown>
          </Menu>
          <Button size="xs" variant="subtle" color="red" onClick={onClear}>
            Clear
          </Button>
        </Group>
      </Group>
    </Box>
  );
}
