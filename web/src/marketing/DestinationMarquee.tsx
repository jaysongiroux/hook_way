import { Box, Container, Stack, Text } from "@mantine/core";
import { useVisibleMotion } from "./useVisibleMotion";
import { DEST_TYPES } from "../graph/nodeSchema";
import { DARK } from "../tokens";
import { DestinationIdentity } from "./destinationIdentity";
import {
  MARQUEE_GROUP_CLASS,
  MARQUEE_SHELL_CLASS,
  MARQUEE_TRACK_CLASS,
  MARQUEE_VIEWPORT_CLASS,
} from "./marketingStyles";

export function DestinationMarquee() {
  const { viewportRef, running } = useVisibleMotion();
  return (
    <Box
      component="section"
      aria-labelledby="destination-marquee-title"
      py={32}
      style={{
        background: DARK.surface,
        borderTop: `1px solid ${DARK.border}`,
        borderBottom: `1px solid ${DARK.border}`,
      }}
    >
      <Container size="lg">
        <Box className={MARQUEE_SHELL_CLASS}>
          <Box className="hookway-marquee-heading">
            <Stack gap={6}>
              <Text
                id="destination-marquee-title"
                component="h2"
                size="xl"
                fw={650}
                c={DARK.text}
                m={0}
              >
                Send events to {DEST_TYPES.length} destination types.
              </Text>
              <Text size="sm" c={DARK.muted} m={0}>
                Declare your destinations once. Connect them in any flow.
              </Text>
            </Stack>
            <Text size="sm" c={DARK.muted} m={0} maw={350}>
              Start Temporal workflows; your workers process the events.
            </Text>
          </Box>
          <Box ref={viewportRef} className={MARQUEE_VIEWPORT_CLASS}>
            <Box
              className={MARQUEE_TRACK_CLASS}
              data-running={running ? "true" : "false"}
            >
              <Box component="span" className={MARQUEE_GROUP_CLASS} role="list">
                {DEST_TYPES.map((type) => (
                  <DestinationIdentity key={type} type={type} role="listitem" />
                ))}
              </Box>
              <Box
                component="span"
                className={MARQUEE_GROUP_CLASS}
                aria-hidden="true"
              >
                {DEST_TYPES.map((type) => (
                  <DestinationIdentity key={type} type={type} />
                ))}
              </Box>
            </Box>
          </Box>
        </Box>
      </Container>
    </Box>
  );
}
