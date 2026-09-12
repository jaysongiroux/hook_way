import { Anchor, Box, Container, Group, Text } from "@mantine/core";
import { Link } from "react-router-dom";
import { DOC_LINKS, GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

const CONFIG_DOC = DOC_LINKS.find((d) => d.path === "docs/configuration.md")!;

export function SiteHeader() {
  return (
    <Box
      component="header"
      style={{
        borderBottom: `1px solid ${DARK.border}`,
        background: "rgba(21, 32, 51, 0.85)",
        backdropFilter: "blur(8px)",
        position: "sticky",
        top: 0,
        zIndex: 10,
      }}
    >
      <Container size="lg">
        <Group h={60} justify="space-between">
          <Group gap={8}>
            <Box style={{ width: 10, height: 10, borderRadius: 3, background: DARK.accent }} />
            <Text fw={700} c={DARK.text} size="md">
              Hookway
            </Text>
          </Group>
          <Group gap="lg">
            <Anchor href={CONFIG_DOC.href} target="_blank" rel="noopener noreferrer" size="sm" c={DARK.muted}>
              Docs
            </Anchor>
            <Anchor href={GITHUB_REPO} target="_blank" rel="noopener noreferrer" size="sm" c={DARK.muted}>
              Source
            </Anchor>
            <Anchor component={Link} to="/builder" size="sm" fw={600} c={DARK.accentText}>
              Builder
            </Anchor>
          </Group>
        </Group>
      </Container>
    </Box>
  );
}
