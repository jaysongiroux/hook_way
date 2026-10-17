import { Box, Group, Stack, Text, Title } from "@mantine/core";
import { Section } from "./Section";
import { DARK } from "../tokens";

// A complete flow with a shared destination connection. This is a whole config,
// not an excerpt: `hookway validate` accepts it verbatim (with
// GITHUB_WEBHOOK_SECRET exported), which is what the "validated before it
// ships" label below claims. Removing the server block or the rate_limit node
// makes it fail validation, so keep them.
const LINES: { text: string; kind: "key" | "value" | "comment" | "plain" }[] = [
  { text: "server:", kind: "key" },
  { text: '  addr: ":8080"', kind: "value" },
  { text: "  max_body_bytes: 1048576", kind: "value" },
  { text: "flows:", kind: "key" },
  { text: "  github:", kind: "key" },
  { text: "    nodes:", kind: "key" },
  {
    text: "      - {id: in, type: http_trigger, path: /webhooks/github}",
    kind: "value",
  },
  {
    text: "      - {id: rl, type: rate_limit, rpm: 600, burst: 60}",
    kind: "value",
  },
  {
    text: "      - {id: body, type: read_body, max_bytes: 1048576}",
    kind: "value",
  },
  { text: "      - id: auth", kind: "value" },
  { text: "        type: auth", kind: "value" },
  { text: "        scheme: hmac_sha256_hex", kind: "value" },
  { text: "        signature_header: X-Hub-Signature-256", kind: "value" },
  { text: "        secret: ${GITHUB_WEBHOOK_SECRET}", kind: "value" },
  { text: "      - {id: env, type: envelope}", kind: "value" },
  {
    text: "      - {id: out, type: publish, destination: orders, target: github-events}",
    kind: "value",
  },
  {
    text: "    edges: [in>rl, rl>body, body>auth, auth>env, env>out]",
    kind: "value",
  },
  { text: "destinations:", kind: "key" },
  { text: "  orders:", kind: "key" },
  { text: "    type: webhook", kind: "value" },
  { text: '    webhook: {url: "https://example.com/events"}', kind: "value" },
];

const COLOR = {
  key: DARK.accent,
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
      title="Define the flow in YAML"
      lead="Follow one request from configuration to delivery. The diagram matches the node IDs and edges in this YAML."
    >
      <Box className="hookway-yaml-example">
        <Box
          className="hookway-yaml-source"
          style={{
            border: `1px solid ${DARK.border}`,
            borderRadius: 4,
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
            <Text
              size="xs"
              c={DARK.muted}
              style={{ fontFamily: "ui-monospace, Menlo, monospace" }}
            >
              hookway.yaml
            </Text>
            <Text size="xs" c={DARK.muted}>
              validated before it ships
            </Text>
          </Group>
          <Box
            px="md"
            py="md"
            style={{ overflowX: "auto" }}
            tabIndex={0}
            role="region"
            aria-label="Example Hookway configuration"
          >
            <Stack gap={2}>
              {LINES.map((l) => (
                <Text
                  key={l.text || `blank-${LINES.indexOf(l)}`}
                  size="xs"
                  c={COLOR[l.kind]}
                  style={{
                    fontFamily:
                      "ui-monospace, SFMono-Regular, Menlo, monospace",
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
        <Box
          component="figure"
          className="hookway-yaml-flow"
          m={0}
          aria-label="The github YAML flow: in to rl to body to auth to env to out"
        >
          <Title order={3} size="h5">
            One GitHub webhook, end to end
          </Title>
          <ol className="hookway-node-path" data-marketing-motion="sequence">
            {[
              ["in", "Receive", "POST /webhooks/github"],
              ["rl", "Rate limit", "600 requests/min · burst of 60"],
              ["body", "Read the body", "Up to 1 MiB"],
              [
                "auth",
                "Verify the signature",
                "HMAC SHA-256 · your webhook secret",
              ],
              [
                "env",
                "Build the envelope",
                "Wrap the verified event for delivery",
              ],
              ["out", "Publish to orders", "https://example.com/events"],
            ].map(([id, label, detail]) => (
              <li key={id}>
                <code>{id}</code>
                <Box>
                  <Text fw={600} size="sm">
                    {label}
                  </Text>
                  <Text size="xs" c={DARK.muted}>
                    {detail}
                  </Text>
                </Box>
              </li>
            ))}
          </ol>
          <Text component="figcaption" size="sm" c={DARK.muted}>
            The <code>edges</code> connect these node IDs in order. Requests
            that fail a limit or signature check stop before publication.
          </Text>
        </Box>
      </Box>
    </Section>
  );
}
