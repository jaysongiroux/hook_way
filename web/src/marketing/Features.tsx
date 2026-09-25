import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { Section } from "./Section";
import { DARK } from "../tokens";

const FEATURES: { span: 2 | 3 | 4 | 6; title: string; body: string }[] = [
  {
    span: 3,
    title: "Publish to 14 destination types",
    body: "Queues, streams, databases, object storage, Temporal, webhook, and Lambda. `publish.payload: object` stores decoded file bytes on S3 and redis_kv. Named connections can hold shared destination credentials. Webhook, Kafka, and RabbitMQ retry with backoff; only webhook can dead-letter a failed publish.",
  },
  {
    span: 3,
    title: "Verify 11 wire-format schemes",
    body: "HMAC layouts, HTTP Basic, an exact header, and JWT algorithms HS256, RS256, and ES256. An unknown scheme fails closed.",
  },
  {
    span: 2,
    title: "JSON, XML, multipart, and raw bodies",
    body: "XML elements and multipart file metadata stay addressable in routing conditions; raw bytes stay forwardable.",
  },
  {
    span: 2,
    title: "Branch on request data",
    body: "Use filter to stop a path, if/else for two outcomes, or switch for the first matching labelled route.",
  },
  {
    span: 2,
    title: "Transform the payload",
    body: "field_map, changed_fields, xml_prune, sandboxed JavaScript, or http_request.",
  },
];

export function Features() {
  return (
    <Section
      title="Configure the full request path"
      lead="Publish into queues, streams, databases, object storage, Temporal, webhook, and Lambda. Verification fails closed on an unknown scheme."
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
