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
import { GITHUB_REPO } from "./links";
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
            Route webhooks.
            <br /> Serve HTTP routes.
            <br /> Expose MCP tools.
          </Title>
          <Stack className="hookway-hero-copy" gap={24}>
            <Text c={DARK.muted} className="hookway-hero-lead">
              Verify and route webhooks. Handle synchronous HTTP requests as a
              reverse proxy or native flow. Expose selected APIs through{" "}
              <Anchor href="#mcp-gateway" inherit c={DARK.accent}>
                authenticated MCP endpoints
              </Anchor>{" "}
              and let compatible agents subscribe to business events.
            </Text>
            <Group gap="sm">
              <Button component={Link} to="/builder" size="md" color="brand">
                Open the builder
              </Button>
              <Button
                component="a"
                href={GITHUB_REPO}
                target="_blank"
                rel="noopener noreferrer"
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
                View on GitHub
              </Button>
            </Group>
            <Text size="sm" c={DARK.muted}>
              Self-hosted. Configured in YAML. Built for the systems you run.
            </Text>
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
