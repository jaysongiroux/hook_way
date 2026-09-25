import { Box, Container, Text } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { DEST_TYPES } from "../graph/nodeSchema";
import { DARK } from "../tokens";
import { DestinationIdentity } from "./destinationIdentity";
import {
  MARQUEE_GROUP_CLASS,
  MARQUEE_SHELL_CLASS,
  MARQUEE_TRACK_CLASS,
  MARQUEE_VIEWPORT_CLASS,
} from "./marketingStyles";

function useMarqueeRunning() {
  const viewportRef = useRef<HTMLDivElement>(null);
  const [inView, setInView] = useState(true);
  const [pageVisible, setPageVisible] = useState(() => typeof document === "undefined" || !document.hidden);

  useEffect(() => {
    const onVisibilityChange = () => setPageVisible(!document.hidden);
    document.addEventListener("visibilitychange", onVisibilityChange);

    const target = viewportRef.current;
    const observer =
      target && typeof IntersectionObserver !== "undefined"
        ? new IntersectionObserver(([entry]) => setInView(entry.isIntersecting))
        : undefined;
    if (target) observer?.observe(target);

    return () => {
      document.removeEventListener("visibilitychange", onVisibilityChange);
      observer?.disconnect();
    };
  }, []);

  return { viewportRef, running: inView && pageVisible };
}

export function DestinationMarquee() {
  const { viewportRef, running } = useMarqueeRunning();
  return (
    <Box
      component="section"
      aria-labelledby="destination-marquee-title"
      py={16}
      style={{ background: DARK.surface, borderTop: `1px solid ${DARK.border}`, borderBottom: `1px solid ${DARK.border}` }}
    >
      <Container size="lg">
        <Box className={MARQUEE_SHELL_CLASS}>
          <Text id="destination-marquee-title" component="h2" size="sm" fw={650} c={DARK.text} m={0}>
            Declare once in YAML. Publish to {DEST_TYPES.length} destination types.
          </Text>
          <Box ref={viewportRef} className={MARQUEE_VIEWPORT_CLASS}>
            <Box className={MARQUEE_TRACK_CLASS} data-running={running ? "true" : "false"}>
              <Box component="span" className={MARQUEE_GROUP_CLASS} role="list">
                {DEST_TYPES.map((type) => (
                  <DestinationIdentity key={type} type={type} role="listitem" />
                ))}
              </Box>
              <Box component="span" className={MARQUEE_GROUP_CLASS} aria-hidden="true">
                {DEST_TYPES.map((type) => (
                  <DestinationIdentity key={type} type={type} />
                ))}
              </Box>
            </Box>
          </Box>
          {/* Temporal is a catalog type, but workflow settings are not fields
              on publish and standalone YAML cannot set them. Say so beside the
              count so the marquee is not read as full Temporal support. */}
          <Text size="sm" c={DARK.muted} m={0}>
            Temporal workflow settings are not yet exposed in standalone YAML.
          </Text>
        </Box>
      </Container>
    </Box>
  );
}
