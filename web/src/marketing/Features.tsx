import { Box, Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { Section } from "./Section";
import { DARK } from "../tokens";

// Spans are deliberately uneven: the two capabilities that differentiate
// Hookway get the wide tiles, the rest sit in narrower ones.
const FEATURES: { span: 2 | 3 | 4 | 6; accent: string; title: string; body: string }[] = [
  {
    span: 3,
    accent: DARK.accent,
    title: "Verified on arrival",
    body: "HMAC signatures, static header matching, or JWT bearer tokens, chosen per endpoint and named by wire format rather than by vendor.",
  },
  {
    span: 3,
    accent: "#7070FF",
    title: "Fan out anywhere",
    body: "One event reaches every destination you route it to, each with its own target and its own filters.",
  },
  { span: 2, accent: "#25BE8A", title: "Provisioned for you", body: "Queues, topics and streams created on first use." },
  { span: 2, accent: DARK.highlight, title: "Routed per request", body: "Targets filled from a header, path, body or lookup." },
  { span: 2, accent: "#35B2AD", title: "Safe under load", body: "Allowlists, rate limits and replay protection." },
];

export function Features() {
  return (
    <Section
      eyebrow="Capabilities"
      title="Everything between the sender and your systems"
      lead="Hookway sits in front of your infrastructure and does the work you would otherwise write by hand for every integration."
    >
      <Bento>
        {FEATURES.map((f) => (
          <Tile key={f.title} span={f.span}>
            <Stack gap={10}>
              <Box style={{ width: 28, height: 4, borderRadius: 2, background: f.accent }} />
              <Text fw={600} c={DARK.text} size="md">
                {f.title}
              </Text>
              <Text size="sm" c={DARK.muted}>
                {f.body}
              </Text>
            </Stack>
          </Tile>
        ))}
      </Bento>
    </Section>
  );
}
