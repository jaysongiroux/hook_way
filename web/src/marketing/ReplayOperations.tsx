import { Anchor, Box, Group, Text, Title } from "@mantine/core";
import { DARK } from "../tokens";
import { docsHref } from "./links";
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
      <Box component="ol" className="hookway-replay-steps">
        {WORKFLOW.map(({ title, body }) => (
          <li key={title}>
            <Title order={3} size="h5" c={DARK.text}>
              {title}
            </Title>
            <Text size="sm" c={DARK.muted} mt={8}>
              {body}
            </Text>
          </li>
        ))}
      </Box>
      <Group mt={24}>
        <Anchor href={docsHref("docs/operations/replay-console.md")}>
          Deploy the replay console
        </Anchor>
      </Group>
      <Box className="hookway-section-note">
        <Title order={3} size="h5">
          Just need to inspect a sample?
        </Title>
        <Text size="sm" c={DARK.muted} mt={8} maw={760}>
          For spot checks, put Sample → Inspect on a separate branch while
          Publish receives every event. Inspections are view only: see the
          current body and context, or download captured files. Uploads are best
          effort and share replay storage and retention limits.{" "}
          <Anchor href={docsHref("examples/inspect.yaml")}>
            See the sampled inspection example
          </Anchor>
          .
        </Text>
      </Box>
    </Section>
  );
}
