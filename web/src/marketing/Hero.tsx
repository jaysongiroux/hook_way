import { Badge, Box, Button, Container, Group, Stack, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import { DEST_TYPES } from "../graph/destTypes";
import { BuilderFrame } from "./BuilderFrame";
import { GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

export function Hero() {
  return (
    <Box
      component="section"
      pt={72}
      pb={80}
      style={{
        background: DARK.bg,
        // A single soft accent glow behind the headline gives the canvas depth
        // without a gradient wash over the whole page.
        backgroundImage:
          "radial-gradient(900px 420px at 22% -8%, rgba(54, 108, 237, 0.22), transparent 70%)",
      }}
    >
      <Container size="lg">
        <Stack align="center" gap="lg" mb={56}>
          <Badge
            size="md"
            radius="sm"
            variant="outline"
            tt="none"
            styles={{ root: { borderColor: DARK.border, color: DARK.muted, background: "transparent" } }}
          >
            Self-hosted · {DEST_TYPES.length} destination types
          </Badge>
          <Title
            order={1}
            c={DARK.text}
            ta="center"
            style={{ fontSize: 60, lineHeight: 1.05, letterSpacing: -1.6, maxWidth: 780 }}
          >
            Webhooks in,{" "}
            <Text component="span" inherit c={DARK.accentText}>
              anywhere out.
            </Text>
          </Title>
          <Text size="lg" c={DARK.muted} ta="center" maw={620}>
            Verify every incoming webhook, then publish it to the queues, streams, databases and services you already
            run. One YAML file, no glue code.
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
              styles={{ root: { background: "transparent", borderColor: DARK.border, color: DARK.text } }}
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
