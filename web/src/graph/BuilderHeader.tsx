import { Box, Button, Group, Menu, Text } from "@mantine/core";
import type { ReactNode } from "react";
import { TbChevronDown } from "react-icons/tb";
import { Logo } from "../brand/Logo";
import { HEADER_HEIGHT, HEADER_STYLE, TOOLBAR_DIVIDER } from "../brand/chrome";
import { DARK } from "../tokens";
import { FlowSwitcher } from "./FlowSwitcher";

export function BuilderHeader({
  actions,
  paletteOpen,
  onTogglePalette,
  onInspector,
  onFlowSettings,
  onImport,
  onOpenAPI,
  onExport,
  onDestinations,
  onConnections,
  onRouting,
  onCredentials,
  onLookups,
 onLaunchDarkly,
  onStateStores,
  onEnv,
  onServer,
  onReplay,
  onLoadExample,
  onClear,
}: {
  actions?: ReactNode;
  paletteOpen: boolean;
  onTogglePalette: () => void;
  onInspector: () => void;
  onFlowSettings: () => void;
  onImport: () => void;
  onOpenAPI?: () => void;
  onExport: () => void;
  onDestinations: () => void;
  onConnections: () => void;
  onRouting?: () => void;
  onCredentials?: () => void;
  onLookups?: () => void;
 onLaunchDarkly?: () => void;
  onStateStores?: () => void;
  onEnv: () => void;
  onServer: () => void;
  onReplay: () => void;
  onLoadExample: () => void;
  onClear: () => void;
}) {
  return (
    <Box component="header" px="md" style={{ ...HEADER_STYLE, flexShrink: 0 }}>
      <Group
        mih={HEADER_HEIGHT}
        justify="space-between"
        align="center"
        wrap="wrap"
        gap="xs"
        style={{ width: "100%" }}
      >
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <Logo to="/" />
          <Text size="sm" c={DARK.muted} fw={500} visibleFrom="sm">
            Builder
          </Text>
          <Box aria-hidden visibleFrom="sm" style={TOOLBAR_DIVIDER} />
          <FlowSwitcher />
        </Group>
        <Group gap="xs" wrap="wrap">
          <Button.Group>
            <Button
              size="xs"
              variant={paletteOpen ? "filled" : "default"}
              aria-pressed={paletteOpen}
              onClick={onTogglePalette}
            >
              Nodes
            </Button>
            {/* Not a toggle: the inspector is a modal, so it has no
                persistent pressed state to advertise. */}
            <Button size="xs" variant="default" onClick={onInspector}>
              Inspector
            </Button>
          </Button.Group>
          <Box aria-hidden visibleFrom="sm" style={TOOLBAR_DIVIDER} />
          <Menu position="bottom-end" shadow="md">
            <Menu.Target>
              <Button
                size="xs"
                variant="default"
                rightSection={<TbChevronDown aria-hidden size={14} />}
                styles={{ section: { marginInlineStart: 6 } }}
              >
                Config
              </Button>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Label>Flow</Menu.Label>
              <Menu.Item onClick={onFlowSettings}>Flow settings</Menu.Item>
              <Menu.Divider />
              <Menu.Label>YAML</Menu.Label>
              <Menu.Item onClick={onImport}>Import YAML</Menu.Item>
              <Menu.Item onClick={onOpenAPI}>Create MCP tools from OpenAPI</Menu.Item>
              <Menu.Item onClick={onExport}>Export YAML</Menu.Item>
              <Menu.Divider />
              <Menu.Item onClick={onLoadExample}>
                Load production example
              </Menu.Item>
              <Menu.Divider />
              <Menu.Label>Workspace</Menu.Label>
              <Menu.Item onClick={onConnections}>Connections</Menu.Item>
              <Menu.Item onClick={onDestinations}>Destinations</Menu.Item>
              <Menu.Item onClick={onRouting}>Routing</Menu.Item>
              <Menu.Item onClick={onCredentials}>Credentials</Menu.Item>
              <Menu.Item onClick={onLookups}>Lookups</Menu.Item>
 <Menu.Item onClick={onLaunchDarkly}>LaunchDarkly</Menu.Item>
              <Menu.Item onClick={onStateStores}>Protection stores</Menu.Item>
              <Menu.Item onClick={onEnv}>Environment</Menu.Item>
              <Menu.Item onClick={onServer}>Core server</Menu.Item>
              <Menu.Item onClick={onReplay}>Replay service</Menu.Item>
            </Menu.Dropdown>
          </Menu>
          <Button size="xs" variant="subtle" color="red" onClick={onClear}>
            Clear
          </Button>
          {actions && <>
            <Box aria-hidden visibleFrom="sm" style={TOOLBAR_DIVIDER} />
            {actions}
          </>}
        </Group>
      </Group>
    </Box>
  );
}
