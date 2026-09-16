import { Box, Group, Stack, Text } from "@mantine/core";
import { Section } from "./Section";
import { DARK } from "../tokens";

// A complete V2 flow with a shared destination connection.
const LINES: { text: string; kind: "key" | "value" | "comment" | "plain" }[] = [
  { text: "schema_version: 2", kind: "key" },
  { text: "flows:", kind: "key" },
  { text: "  github:", kind: "key" },
  { text: "    nodes:", kind: "key" },
  { text: "      - {id: in, type: http_trigger, path: /webhooks/github}", kind: "value" },
  { text: "      - {id: body, type: read_body, max_bytes: 1048576}", kind: "value" },
  { text: "      - id: auth", kind: "value" },
  { text: "        type: auth", kind: "value" },
  { text: "        scheme: hmac_sha256_hex", kind: "value" },
  { text: "        signature_header: X-Hub-Signature-256", kind: "value" },
  { text: "        secret: ${GITHUB_WEBHOOK_SECRET}", kind: "value" },
  { text: "      - {id: env, type: envelope}", kind: "value" },
  { text: "      - {id: out, type: publish, destination: orders, target: github-events}", kind: "value" },
  { text: "    edges: [in>body, body>auth, auth>env, env>out]", kind: "value" },
  { text: "destinations:", kind: "key" },
  { text: "  orders:", kind: "key" },
  { text: "    type: webhook", kind: "value" },
  { text: "    webhook: {url: \"https://example.com/events\"}", kind: "value" },
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
      title="Define the flow in YAML"
      lead="No SDK to install and no handler to deploy. Declare the flow, the verification, and where events should land."
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
