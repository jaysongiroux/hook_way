import { Box, Container, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";
import { DARK } from "../tokens";

// Section is the shared band: an accent eyebrow, a heading, an optional lead,
// and content. Every band on the page uses it, so the vertical rhythm and the
// type scale are defined in exactly one place.
export function Section({
  eyebrow,
  title,
  lead,
  children,
  background,
}: {
  eyebrow: string;
  title: string;
  lead?: string;
  children: ReactNode;
  background?: string;
}) {
  return (
    <Box component="section" py={72} style={{ background: background ?? DARK.bg }}>
      <Container size="lg">
        <Text
          size="xs"
          fw={600}
          c={DARK.accentText}
          mb={10}
          style={{ letterSpacing: 1.4, textTransform: "uppercase" }}
        >
          {eyebrow}
        </Text>
        <Title order={2} c={DARK.text} style={{ fontSize: 34, letterSpacing: -0.4 }}>
          {title}
        </Title>
        {lead && (
          <Text size="md" c={DARK.muted} mt="sm" maw={620}>
            {lead}
          </Text>
        )}
        <Box mt={36}>{children}</Box>
      </Container>
    </Box>
  );
}
