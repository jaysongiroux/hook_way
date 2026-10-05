import { Box, SimpleGrid, Stack, Text, UnstyledButton } from "@mantine/core";
import { DEST_TYPES } from "./nodeSchema";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import { DEST_TILE_CLASS } from "./builderStyles";

/**
 * The type grid stands in for the editor while adding, and is what an empty
 * config opens on. Picking the backend first is what lets the editor show one
 * destination at a time: there is no half-made row to name before you know
 * what it is.
 *
 * The tiles are driven by DEST_TYPES, so a backend added to
 * destinationSchema.json appears here without being listed twice; only its
 * icon comes from the presentation table, which falls back to a generic glyph.
 */
export function DestinationTypePicker({ onPick }: { onPick: (type: string) => void }) {
  return (
    <Stack gap="sm">
      <Text fw={600} size="xs" tt="uppercase" c="dimmed" style={{ letterSpacing: "0.08em" }}>
        Choose a destination type
      </Text>
      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="xs">
        {DEST_TYPES.map((type) => {
          const { label, icon: Icon } = destinationPresentation(type);
          return (
            <UnstyledButton key={type} className={DEST_TILE_CLASS} onClick={() => onPick(type)}>
              <Stack gap={6} align="center" p="sm">
                <Box component="span" c="dimmed" style={{ lineHeight: 0 }}>
                  <Icon aria-hidden="true" focusable="false" size={22} />
                </Box>
                <Text size="xs" fw={500} ta="center">
                  {label}
                </Text>
              </Stack>
            </UnstyledButton>
          );
        })}
      </SimpleGrid>
    </Stack>
  );
}
