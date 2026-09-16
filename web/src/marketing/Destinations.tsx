import { Box, Group, Text } from "@mantine/core";
import { DEST_TYPES } from "../graph/nodeSchema";
import { Section } from "./Section";
import { DARK } from "../tokens";

// Read from the builder's destination registry, so this can never drift from
// what the product actually supports.
export function Destinations() {
  return (
    <Section
      title={`${DEST_TYPES.length} destination types, one way to configure them`}
      lead="Declare once, reference from publish nodes. Every type is validated before you ship it and provisioned when a publish sets create: true."
    >
      <Group gap={10}>
        {DEST_TYPES.map((t) => (
          <Box
            key={t}
            px={12}
            py={7}
            style={{
              border: `1px solid ${DARK.border}`,
              borderRadius: 4,
              background: DARK.surface,
              fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
            }}
          >
            <Text size="sm" c={DARK.text} style={{ fontFamily: "inherit" }}>
              {t}
            </Text>
          </Box>
        ))}
      </Group>
    </Section>
  );
}
