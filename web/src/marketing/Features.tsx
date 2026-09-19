import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { Section } from "./Section";
import { DARK } from "../tokens";

const FEATURES: { span: 2 | 3 | 4 | 6; title: string; body: string }[] = [
  {
    span: 3,
    title: "JSON, XML, multipart, and raw bodies",
    body: "Choose the body format each flow expects. XML elements and multipart file metadata remain addressable in routing conditions; raw bytes stay available for forwarding.",
  },
  {
    span: 3,
    title: "Branch on request data",
    body: "Use filter to stop a path, if/else for two outcomes, or switch for the first matching labelled route. The builder exposes every branch before export.",
  },
  {
    span: 2,
    title: "Verify before parsing",
    body: "HMAC, static header, or JWT verification runs against the raw request and fails closed.",
  },
  {
    span: 2,
    title: "Transform or proxy",
    body: "Use field maps, changed-field diffs, XML pruning, sandboxed JavaScript, or a controlled HTTP request.",
  },
  {
    span: 2,
    title: "Publish to existing infrastructure",
    body: "Each publish node names a configured destination and target. Webhook, Kafka, and RabbitMQ add retries and dead-letter handling.",
  },
];

export function Features() {
  return (
    <Section
      title="Configure the full request path"
      lead="The YAML graph records verification, parsing, branches, transforms, and delivery in one place. Application services do not need provider-specific webhook handlers."
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
