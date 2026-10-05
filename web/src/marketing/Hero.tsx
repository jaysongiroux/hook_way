import { Box, Button, Container, Group, Stack, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import { BuilderFrame } from "./BuilderFrame";
import { GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

export function Hero() {
  return (
    <Box component="section" className="hookway-overview-hero" style={{ background: DARK.bg }}>
      <Container size="lg">
        <Box className="hookway-hero-intro">
          <Title order={1} c={DARK.text} className="hookway-hero-title">
            Receive, verify,<br />{" "}
            and route webhooks.
          </Title>
          <Stack className="hookway-hero-copy" gap={24}>
            <Text c={DARK.muted} className="hookway-hero-lead">
              Check who sent each request, prepare its data, and send it straight
              to the queues, databases, or APIs your team already uses.
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
                styles={{ root: { background: DARK.surface, borderColor: DARK.border, color: DARK.text } }}
              >
                View on GitHub
              </Button>
            </Group>
            <Text size="sm" c={DARK.muted}>
              Self-hosted. Configured in YAML. Built for the systems you run.
            </Text>
          </Stack>
        </Box>
        <BuilderFrame />
        <Text size="sm" c={DARK.muted} className="hookway-hero-caption">
          Design a flow, export the YAML, and deploy it in your infrastructure.
          The builder does not connect to or manage your running server.
        </Text>
      </Container>
    </Box>
  );
}
