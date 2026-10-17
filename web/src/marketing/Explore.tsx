import { Anchor, Box, Stack, Text } from "@mantine/core";
import { Link } from "react-router-dom";
import { Section } from "./Section";
import {
  EXPLORE_GRID_CLASS,
  EXPLORE_PRIMARY_CLASS,
  EXPLORE_SECONDARY_CLASS,
} from "./marketingStyles";
import { DARK } from "../tokens";

const MONO = "var(--mantine-font-family-monospace)";

// The overview stays concise, but these two paths let an evaluator move from
// capabilities to runnable evidence or to a sourced product comparison.
export function Explore() {
  return (
    <Section
      title="See Hookway in context"
      lead="Explore working examples or compare Hookway with other webhook and API tools."
    >
      <Box className={EXPLORE_GRID_CLASS}>
        <Stack className={EXPLORE_PRIMARY_CLASS} gap="sm">
          <Text component="h3" fw={650} c={DARK.text} style={{ fontSize: 22, lineHeight: 1.25, margin: 0 }}>
            Use request data to choose the destination
          </Text>
          <Text c={DARK.muted} style={{ lineHeight: 1.6 }}>
            Examples show how to store uploaded files, clean up XML, send one event to several systems, and choose destinations from request data. Variables can come
            from a path, JSON body, or filtered header, then select a target such as{" "}
            <Text component="code" inherit c={DARK.text} style={{ fontFamily: MONO }}>
              {"{envName}-events"}
            </Text>.
          </Text>
          <Anchor component={Link} to="/use-cases" fw={650} c={DARK.accent} underline="always">
            Explore use cases
          </Anchor>
        </Stack>

        <Stack className={EXPLORE_SECONDARY_CLASS} gap="sm">
          <Text component="h3" fw={650} c={DARK.text} style={{ fontSize: 18, lineHeight: 1.3, margin: 0 }}>
            Compare Hookway with other products
          </Text>
          <Text size="sm" c={DARK.muted} style={{ lineHeight: 1.6 }}>
            Compare deployment, delivery and MCP tools with Hookdeck Event Gateway and Convoy. Includes source links and a separate look at Outpost.
          </Text>
          <Anchor component={Link} to="/compare" fw={650} c={DARK.accent} underline="always">
            View the comparison
          </Anchor>
        </Stack>
      </Box>
    </Section>
  );
}
