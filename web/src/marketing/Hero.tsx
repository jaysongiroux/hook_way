import {
  Anchor,
  Box,
  Button,
  Container,
  Group,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { Link } from "react-router-dom";
import { BuilderFrame } from "./BuilderFrame";
import { docsHref } from "./links";
import { DARK } from "../tokens";

export function Hero() {
  return (
    <Box
      component="section"
      className="hookway-overview-hero"
      style={{ background: DARK.bg }}
    >
      <Container size="lg">
        <Box className="hookway-hero-intro" data-marketing-motion="intro">
          <Title order={1} c={DARK.text} className="hookway-hero-title">
            Verify and route webhooks.
            <br /> In your infrastructure.
          </Title>
          <Stack className="hookway-hero-copy" gap={24}>
            <Text c={DARK.muted} className="hookway-hero-lead">
              Receive signed webhooks and deliver verified events to your
              queues, streams, databases and APIs. Also handle HTTP requests and
              expose selected APIs through{" "}
              <Anchor href="#mcp-gateway" inherit c={DARK.accent}>
                authenticated MCP endpoints
              </Anchor>{" "}
              and let compatible agents subscribe to business events.
            </Text>
            <Group gap="sm">
              <Button
                component={Link}
                to={docsHref("docs/get-started.md")}
                size="md"
                color="brand"
              >
                Get started
              </Button>
              <Button
                component={Link}
                to="/builder"
                size="md"
                variant="default"
                styles={{
                  root: {
                    background: DARK.surface,
                    borderColor: DARK.border,
                    color: DARK.text,
                  },
                }}
              >
                Try the builder
              </Button>
            </Group>
          </Stack>
        </Box>
        <Box data-marketing-motion="visual">
          <BuilderFrame />
        </Box>
        <Text size="sm" c={DARK.muted} className="hookway-hero-caption">
          Design a flow, export the YAML, and deploy it in your infrastructure.
          Test deployed MCP tools from the builder; apply configuration changes
          through your deployment process.
        </Text>
      </Container>
    </Box>
  );
}
