import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { Section } from "./Section";
import { DARK } from "../tokens";

const FEATURES: { span: 2 | 3 | 4 | 6; title: string; body: string }[] = [
  {
    span: 3,
    title: "Verified on arrival",
    body: "HMAC signatures, static header matching, or JWT bearer tokens, chosen per flow and named by wire format rather than by vendor.",
  },
  {
    span: 3,
    title: "Fan out anywhere",
    body: "One event reaches every destination you route it to, each with its own target and its own filters.",
  },
  { span: 2, title: "Provisioned for you", body: "Queues, topics and streams created on first use." },
  { span: 2, title: "Routed per request", body: "Targets filled from a path segment or a body field." },
  { span: 2, title: "Safe under load", body: "Allowlists, rate limits and replay protection." },
];

export function Features() {
  return (
    <Section
      title="Everything between the sender and your systems"
      lead="Hookway sits in front of your infrastructure and does the work you would otherwise write by hand for every integration."
    >
      <Bento>
        {FEATURES.map((f) => (
          <Tile key={f.title} span={f.span}>
            <Stack gap={8}>
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
