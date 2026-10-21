import { Box, Container, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";
import { DARK } from "../tokens";

export function Section({
  title,
  lead,
  children,
  background,
}: {
  title: string;
  lead?: string;
  children: ReactNode;
  background?: string;
}) {
  return (
    <Box component="section" py={72} style={{ background: background ?? DARK.bg }}>
      <Container size="lg">
        <Title
          order={2}
          c={DARK.text}
          style={{ fontSize: "clamp(28px, 5vw, 32px)", lineHeight: 1.2, letterSpacing: -0.4, fontWeight: 650 }}
        >
          {title}
        </Title>
        {lead && (
          <Text component="p" mt={14} maw="68ch" c={DARK.muted} style={{ fontSize: 17, lineHeight: 1.65 }}>
            {lead}
          </Text>
        )}
        <Box mt={40}>{children}</Box>
      </Container>
    </Box>
  );
}
