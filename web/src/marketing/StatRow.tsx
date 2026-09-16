import { Box, Container, SimpleGrid, Stack, Text } from "@mantine/core";
import { STATS } from "./stats";
import { DARK } from "../tokens";

export function StatRow() {
  return (
    <Box
      style={{ borderTop: `1px solid ${DARK.border}`, borderBottom: `1px solid ${DARK.border}`, background: DARK.surface }}
      py={28}
    >
      <Container size="lg">
        <SimpleGrid cols={{ base: 2, md: 4 }} spacing="xl">
          {STATS.map((s) => (
            <Stack key={s.label} gap={4}>
              <Text size="sm" fw={600} c={DARK.text} style={{ fontVariantNumeric: "tabular-nums" }}>
                {s.value} {s.label}
              </Text>
              <Text size="xs" c={DARK.muted}>
                {s.detail}
              </Text>
            </Stack>
          ))}
        </SimpleGrid>
      </Container>
    </Box>
  );
}
