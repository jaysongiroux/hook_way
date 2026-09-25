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
            Verify the request by wire format, then publish into the queues, streams, databases, and services
            you already run — from one YAML file. The builder exports that YAML; it does not talk to a running
            process.
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
