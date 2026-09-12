import { Box, Group, Stack, Text } from "@mantine/core";
import { Section } from "./Section";
import { DARK } from "../tokens";

// A trimmed but real config: one endpoint that verifies a signature and fans
// the event out to two destinations.
const LINES: { text: string; kind: "key" | "value" | "comment" | "plain" }[] = [
  { text: "endpoints:", kind: "key" },
  { text: "  github:", kind: "key" },
  { text: "    path: /webhooks/github", kind: "value" },
  { text: "    auth:", kind: "key" },
  { text: "      type: hmac_sha256", kind: "value" },
  { text: "      secret: ${GITHUB_WEBHOOK_SECRET}", kind: "value" },
  { text: "    routes:", kind: "key" },
  { text: "      - destination: orders", kind: "value" },
  { text: "        target: github-events", kind: "value" },
  { text: "      - destination: archive", kind: "value" },
  { text: "        target: raw/github", kind: "value" },
  { text: "", kind: "plain" },
  { text: "destinations:", kind: "key" },
  { text: "  orders:", kind: "key" },
  { text: "    type: sqs", kind: "value" },
  { text: "  archive:", kind: "key" },
  { text: "    type: s3", kind: "value" },
];

const COLOR = {
  key: DARK.highlight,
  value: DARK.text,
  comment: DARK.muted,
  plain: DARK.text,
};

// CodeBand is the config, shown as the product's real interface. Set in a
// recessed frame with mono type, which is how developer tools signal "this is
// the thing you will actually write".
export function CodeBand() {
  return (
    <Section
      eyebrow="Configuration"
      title="The whole integration is one file"
      lead="No SDK to install and no handler to deploy. Declare the endpoint, the verification, and where events should land."
    >
      <Box
        style={{
          border: `1px solid ${DARK.border}`,
          borderRadius: 12,
          background: DARK.recessed,
          overflow: "hidden",
        }}
      >
        <Group
          justify="space-between"
          px="md"
          py={10}
          style={{ borderBottom: `1px solid ${DARK.border}` }}
        >
          <Text size="xs" c={DARK.muted} style={{ fontFamily: "ui-monospace, Menlo, monospace" }}>
            hookway.yaml
          </Text>
          <Text size="xs" c={DARK.muted}>
            validated before it ships
          </Text>
        </Group>
        <Box px="md" py="md" style={{ overflowX: "auto" }} role="img" aria-label="Example Hookway configuration">
          <Stack gap={2}>
            {LINES.map((l) => (
              <Text
                key={l.text || `blank-${LINES.indexOf(l)}`}
                size="sm"
                c={COLOR[l.kind]}
                style={{
                  fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
                  whiteSpace: "pre",
                  lineHeight: 1.7,
                }}
              >
                {l.text}
              </Text>
            ))}
          </Stack>
        </Box>
      </Box>
    </Section>
  );
}
