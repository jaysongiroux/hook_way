import { Anchor, Box, Container, Group, Text } from "@mantine/core";
import { GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

export function SiteFooter() {
  return (
    <Box
      component="footer"
      style={{ borderTop: `1px solid ${DARK.border}`, background: DARK.recessed }}
      py="xl"
    >
      <Container size="lg">
        <Group justify="space-between" gap="sm">
          <Text size="xs" c={DARK.muted}>
            Self-hosted, and it stores nothing you do not configure.
          </Text>
          <Anchor href={GITHUB_REPO} target="_blank" rel="noopener noreferrer" size="xs" c={DARK.muted}>
            StarwingAI/hook_way
          </Anchor>
        </Group>
      </Container>
    </Box>
  );
}
