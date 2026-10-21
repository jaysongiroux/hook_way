import { Anchor, Group, SimpleGrid, Stack, Text, Title } from "@mantine/core";
import { DARK } from "../tokens";
import { docsHref } from "./links";
import { Section } from "./Section";

export function HTTPGateway() {
  return (
    <Section
      title="Handle HTTP requests and return the result"
      lead="Use Hookway as a reverse proxy or a synchronous request handler. Ordinary HTTP clients can call these routes directly."
    >
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing={48}>
        <Stack gap="sm">
          <Title order={3} size="h4" c={DARK.text}>
            Proxy to an upstream API
          </Title>
          <Text c={DARK.muted}>
            Authenticate the caller, select which method, path, headers, query
            parameters and body to forward, then return the upstream status,
            body and allowed response headers on the same connection.
          </Text>
        </Stack>
        <Stack gap="sm">
          <Title order={3} size="h4" c={DARK.text}>
            Build a synchronous handler
          </Title>
          <Text c={DARK.muted}>
            Validate, transform and branch on request data before returning a
            configured status, headers and body. Add API calls where your
            handler needs them. Webhook delivery, synchronous HTTP routes and
            MCP endpoints can share the same Hookway deployment.
          </Text>
        </Stack>
      </SimpleGrid>
      <Group className="hookway-section-note" gap="lg">
        <Anchor href={docsHref("examples/showcase/proxy.yaml")} fw={600}>
          See the reverse proxy example
        </Anchor>
        <Anchor href={docsHref("docs/guides/ingress-adapters.md")} fw={600}>
          Configure HTTP request flows
        </Anchor>
      </Group>
    </Section>
  );
}
