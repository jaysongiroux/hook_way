import { Anchor, Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { PERFORMANCE_SUMMARY, PRODUCTION_CARDS } from "./productionCopy";
import { Section } from "./Section";
import { DARK } from "../tokens";
import { repoBlob } from "./links";

export function Production() {
  return (
    <Section
      title="Control load, monitor delivery, and recover failures"
      lead="Limit active requests, retry supported deliveries, and enable Prometheus metrics to see how your flows are performing. Add the optional replay service when you need to retry missed deliveries."
      background={DARK.recessed}
    >
      <Bento>
        {PRODUCTION_CARDS.map((c) => (
          <Tile key={c.id} span={2}>
            <Stack gap={6}>
              <Text fw={650} c={DARK.text} style={{ fontSize: 28, lineHeight: 1.1, letterSpacing: -0.6, fontVariantNumeric: "tabular-nums" }}>
                {c.value}
              </Text>
              <Text size="xs" c={DARK.muted} fw={500}>
                {c.valueLabel}
              </Text>
              <Text fw={600} c={DARK.text} size="sm" mt={6}>
                {c.title}
              </Text>
              <Text size="sm" c={DARK.muted}>
                {c.line}
              </Text>
            </Stack>
          </Tile>
        ))}
      </Bento>
      <Text size="sm" c={DARK.muted} mt="xl">
        {PERFORMANCE_SUMMARY}{" "}
        <Anchor href={repoBlob("docs/performance.md")} target="_blank" rel="noopener noreferrer">
          Results and methodology
        </Anchor>
        .
      </Text>
    </Section>
  );
}
