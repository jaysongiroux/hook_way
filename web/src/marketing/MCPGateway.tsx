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
        title="Let AI assistants use your APIs"
        lead="Turn selected API operations into tools an AI assistant can discover and call through the Model Context Protocol (MCP). You decide what it can do."
        background={DARK.recessed}
      >
        <SimpleGrid cols={{ base: 1, md: 2 }} spacing={48}>
          <Stack gap={24}>
            <Box component="ol" className="hookway-setup-steps">
              <li>
                <Title order={3} size="h5">
                  Define a tool
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Name the operation, describe it, and define its input and
                  output. For example, check your service’s status.
                </Text>
              </li>
              <li>
                <Title order={3} size="h5">
                  Connect it to your API
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Connect the tool to an HTTP request and return its result. Add
                  native steps to prepare data.
                </Text>
              </li>
              <li>
                <Title order={3} size="h5">
                  Connect your assistant
                </Title>
                <Text c={DARK.muted} mt={8}>
                  Deploy the flow and add its URL to a compatible MCP client.
                  Authenticate with API keys or an external OAuth provider, and
                  grant access per tool.
                </Text>
              </li>
            </Box>
            <Anchor href={docsHref("docs/guides/mcp-gateway.md")} fw={600}>
              Configure the MCP gateway
            </Anchor>
          </Stack>
          <Box
            component="figure"
            m={0}
            className="hookway-mcp-example"
            aria-label="An assistant calls a permitted MCP tool, Hookway requests your API, and returns a validated result"
          >
            <Text component="figcaption" size="sm" c={DARK.muted}>
              Example: check a service from your assistant
            </Text>
            <Box className="hookway-mcp-question">
              <Text size="lg" fw={600}>
                “Is our service healthy?”
              </Text>
              <Text size="sm" c={DARK.muted} mt={4}>
                You ask in your connected assistant.
              </Text>
            </Box>
            <ol className="hookway-tool-path" data-marketing-motion="sequence">
              <li>
                <Text fw={600}>The assistant calls your tool</Text>
                <code>get_service_status</code>
                <Text size="sm" c={DARK.muted}>
                  Hookway checks the caller’s permissions and tool input.
                </Text>
              </li>
              <li>
                <Text fw={600}>Hookway requests your API</Text>
                <code>GET https://status.example.com/status</code>
                <Text size="sm" c={DARK.muted}>
                  Your API credentials stay in Hookway.
                </Text>
              </li>
              <li>
                <Text fw={600}>The result returns to the assistant</Text>
                <code>{'{"status":"ok"}'}</code>
                <Text size="sm" c={DARK.muted}>
                  Hookway validates the output before returning it.
                </Text>
              </li>
            </ol>
            <Anchor
              href={docsHref("examples/templates/mcp-gateway.yaml")}
              size="sm"
            >
              View the authenticated template
            </Anchor>
          </Box>
        </SimpleGrid>
        <Box className="hookway-section-note">
          <Title order={3} size="h5">
            Choose what each assistant can access
          </Title>
          <Text c={DARK.muted} size="sm" mt={8}>
            Endpoints such as <code>/services/mcp</code> and{" "}
            <code>/utilities/mcp</code> each own their tools and permissions.
            Keep upstream credentials separate, set call deadlines, rate limits
            and concurrency limits. Each call runs one selected branch.
          </Text>
          <Group mt={12}>
            <Anchor href={docsHref("examples/mcp-endpoints.yaml")} size="sm">
              See multiple MCP endpoints
            </Anchor>
          </Group>
        </Box>
      </Section>
    </Box>
  );
}
