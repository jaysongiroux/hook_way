import { Box, Container, Title } from "@mantine/core";
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
        <Title order={2} c={DARK.text} style={{ fontSize: 32, letterSpacing: -0.4, fontWeight: 650 }}>
          {title}
        </Title>
        {lead && (
          <Box mt={12} maw={620} c={DARK.muted} style={{ fontSize: 16, lineHeight: 1.5 }}>
            {lead}
          </Box>
        )}
        <Box mt={36}>{children}</Box>
      </Container>
    </Box>
  );
}
