import {
  Anchor,
  Box,
  Group,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { DARK } from "../tokens";
import { docsHref } from "./links";
import { Section } from "./Section";

export function MCPGateway() {
  return (
    <Box id="mcp-gateway" style={{ scrollMarginTop: 88 }}>
      <Section
        title="Give agents tools and events"
        lead="Import APIs as MCP tools. Let compatible agents subscribe to events and look up details when something changes."
        background={DARK.recessed}
      >
        <SimpleGrid cols={{ base: 1, md: 2 }} spacing={48}>
          <Stack gap={24}>
            <Box component="ol" className="hookway-setup-steps">
              <li>
                <Title order={3} size="h5">
                  Import the operations you choose
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Upload an OpenAPI spec, select operations, and review the
                  generated flow before exporting YAML.
                </Text>
              </li>
              <li>
                <Title order={3} size="h5">
                  Set each agent’s access
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Use API keys or an external OAuth provider. Grant tools and
                  events per caller; keep upstream credentials separate.
                </Text>
              </li>
              <li>
                <Title order={3} size="h5">
                  Let events start the next step
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Publish named events from your flows. Subscribed clients
                  receive matching events through signed webhooks.
                </Text>
              </li>
            </Box>
            <Anchor href={docsHref("docs/guides/openapi-import.md")} fw={600}>
              Import an API as MCP tools
            </Anchor>
          </Stack>
          <Box
            component="figure"
            m={0}
            className="hookway-mcp-example"
            aria-label="An issue webhook creates an event, Hookway delivers it to a matching subscriber, and the agent calls get_issue"
          >
            <Text component="figcaption" size="sm" c={DARK.muted}>
              Example: an issue event starts an agent workflow
            </Text>
            <Box className="hookway-mcp-question">
              <Text size="lg" fw={600}>
                “When an issue opens, fetch its details.”
              </Text>
              <Text size="sm" c={DARK.muted} mt={4}>
                For clients that support MCP Events, including ChatGPT Work.
              </Text>
            </Box>
            <ol className="hookway-tool-path" data-marketing-motion="sequence">
              <li>
                <Text fw={600}>A webhook creates a named event</Text>
                <code>issue.opened</code>
                <Text size="sm" c={DARK.muted}>
                  Verify the sender and validate the event payload.
                </Text>
              </li>
              <li>
                <Text fw={600}>Hookway delivers the matching event</Text>
                <code>Signed callback → subscribed agent</code>
                <Text size="sm" c={DARK.muted}>
                  Subscription filters decide which events reach the agent.
                </Text>
              </li>
              <li>
                <Text fw={600}>The agent looks up the details</Text>
                <code>get_issue</code>
                <Text size="sm" c={DARK.muted}>
                  An authenticated tool retrieves details from your API.
                </Text>
              </li>
            </ol>
            <Anchor
              href={docsHref("examples/mcp-events/demo/README.md")}
              size="sm"
            >
              Try the issue event demo
            </Anchor>
          </Box>
        </SimpleGrid>
        <Box className="hookway-section-note">
          <Title order={3} size="h5">
            Inspect and recover event delivery
          </Title>
          <Text c={DARK.muted} size="sm" mt={8}>
            Inspect callback attempts, retry eligible failures, or revoke a
            subscription from the console. Callback acceptance confirms receipt;
            the agent’s work is tracked by its client.
          </Text>
          <Group mt={12}>
            <Anchor href={docsHref("docs/operations/mcp-events-console.md")} size="sm">
              Explore event operations
            </Anchor>
            <Anchor href={docsHref("docs/guides/mcp-events.md")} size="sm">
              Configure MCP Events
            </Anchor>
          </Group>
        </Box>
      </Section>
    </Box>
  );
}
