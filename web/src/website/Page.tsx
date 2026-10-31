import { Anchor, Box, Container, Group, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import type { ReactNode } from "react";
import { MarketingShell } from "../marketing/MarketingShell";
import { DARK } from "../tokens";

export function WebsitePage({
  title,
  lead,
  children,
}: {
  title: string;
  lead: string;
  children: ReactNode;
}) {
  return (
    <MarketingShell>
      <Container size="lg" py={48}>
        <Group component="nav" aria-label="Breadcrumb" gap="xs" mb="xl">
          <Anchor component={Link} to="/">
            Home
          </Anchor>
          <Text aria-hidden="true">/</Text>
          <Text>{title}</Text>
        </Group>
        <Title order={1} maw={900}>
          {title}
        </Title>
        <Text size="xl" c={DARK.muted} maw={900} mt="lg" mb={40}>
          {lead}
        </Text>
        <Box className="hookway-prose">{children}</Box>
      </Container>
    </MarketingShell>
  );
}
