import { Box, SimpleGrid, Stack, Text, UnstyledButton } from "@mantine/core";
import { ConnectionEditor } from "./ConnectionEditor";
import { NamedConfigPanel } from "./NamedConfigPanel";
import { CONN_TYPES } from "./nodeSchema";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { DEST_ROW_CLASS } from "./builderStyles";
import { useGraphStore } from "./store";
import { DARK } from "../tokens";

export function ConnectionsPanel({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const connections = useGraphStore((s) => s.connections);
  const setConnections = useGraphStore((s) => s.setConnections);
  return (
    <NamedConfigPanel
      opened={opened}
      onClose={onClose}
      title="Connections"
      addLabel="Add connection"
      entries={connections}
      onChange={setConnections}
      defaultType="aws"
      createEntry={(type) => ({ type })}
      Picker={ConnectionTypePicker}
      Editor={ConnectionEditor}
      description={
        <>
          Credentials and connection settings, referenced by name from a destination&apos;s{" "}
          <Text span ff="monospace">
            connection
          </Text>{" "}
          field. Keep secrets as{" "}
          <Text span ff="monospace">
            ${"{ENV_VAR}"}
          </Text>{" "}
          references rather than literals. This panel generates config; it does not talk to a running Hookway process.
        </>
      }
    />
  );
}

/**
 * Driven by CONN_TYPES, so a connection type added to connectionSchema.json
 * shows up here without touching this file. Icons are reused from the
 * destination presentation table, which already falls back to a generic mark.
 */
function ConnectionTypePicker({ onPick }: { onPick: (type: string) => void }) {
  return (
    <Stack gap="sm">
      <Text size="sm" fw={600} c={DARK.text}>
        Add a connection
      </Text>
      <SimpleGrid cols={{ base: 2, sm: 3 }} spacing="sm">
        {CONN_TYPES.map((type) => {
          const { label, icon: Icon } = destinationPresentation(type);
          return (
            <UnstyledButton
              key={type}
              className={DEST_ROW_CLASS}
              aria-label={`Add ${type} connection`}
              onClick={() => onPick(type)}
            >
              <Box style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                <Box component="span" c="dimmed" style={{ lineHeight: 0, flexShrink: 0 }}>
                  <Icon aria-hidden="true" focusable="false" size={16} />
                </Box>
                <Text size="xs" truncate>
                  {label}
                </Text>
              </Box>
            </UnstyledButton>
          );
        })}
      </SimpleGrid>
    </Stack>
  );
}
