import { Box, Button, Container, Group, Stack, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import { BuilderFrame } from "./BuilderFrame";
import { GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

export function Hero() {
  return (
    <Box component="section" pt={64} pb={72} style={{ background: DARK.bg }}>
      <Container size="lg">
        <Stack align="center" gap="lg" mb={48}>
          <Title
            order={1}
            c={DARK.text}
            ta="center"
            style={{ fontSize: 44, lineHeight: 1.15, letterSpacing: -0.4, maxWidth: 720, fontWeight: 650 }}
          >
            Receive, verify, and route webhooks.
          </Title>
          <Text size="lg" c={DARK.muted} ta="center" maw={620}>
            Check who sent each request, prepare its data, and send it to the queues,
            databases, or APIs your team already uses. Run Hookway in your own
            infrastructure with one YAML configuration file.
          </Text>
          <Text size="sm" c={DARK.muted} ta="center" maw={620}>
            Design a flow in the builder, export the YAML, then deploy it yourself.
            The builder does not connect to or manage your running server.
          </Text>
          <Group gap="sm" mt="xs">
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
        </Stack>
        <BuilderFrame />
      </Container>
    </Box>
  );
}
