import {
  Anchor,
  Box,
  Group,
  SimpleGrid,
  Stack,
  Text,
  Title,
} from "@mantine/core";
import { Link } from "react-router-dom";
import { DARK } from "../tokens";
import { repoBlob } from "./links";
import { Section } from "./Section";
import replayPreview from "./assets/replay-console.png";

const WORKFLOW = [
  {
    title: "Inspect the failure",
    body: "See the destination, captured error, payload and delivery history together. Filter the inbox to find the events that need attention.",
  },
  {
    title: "Understand the outcome",
    body: "When delivery is uncertain, check the receiving system before sending again. Record independent confirmation and keep the original evidence.",
  },
  {
    title: "Retry that destination",
    body: "Request another attempt for the failed destination without rerunning the whole flow. Follow its result and any downstream failures.",
  },
];

export function ReplayOperations() {
  return (
    <Section
      title="Recover missed deliveries with the replay console"
      lead="The sender got a success response, but a destination missed the event. The optional replay service can retain that failure for your team to investigate and recover. Capture is best effort; it is not a durable queue for every incoming event."
    >
      <Box component="figure" m={0}>
        <Box
          component="img"
          src={replayPreview}
          alt="Hookway replay console showing a filtered failure inbox beside an HTTP delivery error, its destination and recovery actions."
          width={1280}
          height={720}
          loading="lazy"
          style={{
            width: "100%",
            height: "auto",
            display: "block",
            border: `1px solid ${DARK.border}`,
            borderRadius: 4,
          }}
        />
        <Text component="figcaption" size="xs" c={DARK.muted} mt={8}>
          Example delivery records. Operators sign in with a scoped API key;
          your data stays in your deployment.
        </Text>
      </Box>
      <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="xl" mt={32}>
        {WORKFLOW.map(({ title, body }) => (
          <Stack key={title} gap={8}>
            <Title order={3} size="h5" c={DARK.text}>
              {title}
            </Title>
            <Text size="sm" c={DARK.muted}>
              {body}
            </Text>
          </Stack>
        ))}
      </SimpleGrid>
      <Group justify="space-between" align="center" gap="md" mt={32}>
        <Text size="sm" c={DARK.muted} maw={620}>
          The same application includes the public visual builder: create a
          flow, import existing YAML, or export a configuration without signing
          in.
        </Text>
        <Group gap="lg">
          <Anchor
            href={repoBlob("docs/operations/replay-console.md")}
            target="_blank"
            rel="noopener noreferrer"
          >
            Deploy the replay console
          </Anchor>
          <Anchor component={Link} to="/builder">
            Open the public builder
          </Anchor>
        </Group>
      </Group>
    </Section>
  );
}
