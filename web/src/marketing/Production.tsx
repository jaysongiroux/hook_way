import { Box, Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { PRODUCTION_CARDS } from "./productionCopy";
import { Section } from "./Section";
import { DARK } from "../tokens";

// Each tile leads with its number, which is what a reader scanning for
// operational maturity is actually looking for.
export function Production() {
  return (
    <Section
      eyebrow="Production"
      title="The operational work is already done"
      lead="Backpressure, retries and instrumentation ship in the binary. Every figure below is a default, not a roadmap item."
      background={DARK.recessed}
    >
      <Bento>
        {PRODUCTION_CARDS.map((c) => (
          <Tile key={c.id} span={2}>
            <Stack gap={6}>
              <Box style={{ width: 28, height: 4, borderRadius: 2, background: c.accent }} />
              <Text fw={600} c={DARK.text} style={{ fontSize: 38, lineHeight: 1.05, letterSpacing: -1 }}>
                {c.value}
              </Text>
              <Text size="xs" c={DARK.accentText} fw={500}>
                {c.valueLabel}
              </Text>
              <Text fw={600} c={DARK.text} size="sm" mt={4}>
                {c.title}
              </Text>
              <Text size="sm" c={DARK.muted}>
                {c.line}
              </Text>
            </Stack>
          </Tile>
        ))}
      </Bento>
    </Section>
  );
}
