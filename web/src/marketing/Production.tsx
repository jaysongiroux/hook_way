import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { PRODUCTION_CARDS } from "./productionCopy";
import { Section } from "./Section";
import { DARK } from "../tokens";

export function Production() {
  return (
    <Section
      title="Operational controls ship with the binary"
      lead="In-flight limits and publish retries apply by default; Prometheus metrics ship in the binary but must be enabled in config."
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
    </Section>
  );
}
