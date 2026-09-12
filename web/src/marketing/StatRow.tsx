import { Box, Container, SimpleGrid, Stack, Text } from "@mantine/core";
import { STATS } from "./stats";
import { DARK } from "../tokens";

// Four stat tiles, not a chart: the numbers share no scale, so there is nothing
// to plot and no categorical palette to assign. Values wear text ink and the
// font's proportional figures, which read better than tabular at this size.
export function StatRow() {
  return (
    <Box
      style={{ borderTop: `1px solid ${DARK.border}`, borderBottom: `1px solid ${DARK.border}`, background: DARK.recessed }}
      py={36}
    >
      <Container size="lg">
        <SimpleGrid cols={{ base: 2, md: 4 }} spacing="xl">
          {STATS.map((s) => (
            <Stack key={s.label} gap={2}>
              <Text fw={600} c={DARK.text} style={{ fontSize: 40, lineHeight: 1.05, letterSpacing: -1.2 }}>
                {s.value}
              </Text>
              <Text size="sm" fw={500} c={DARK.text}>
                {s.label}
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
