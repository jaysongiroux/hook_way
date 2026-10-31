import { Anchor, Box, Button, Container, Group, Text } from "@mantine/core";
import { Link } from "react-router-dom";
import { GITHUB_REPO } from "./links";
import { DARK } from "../tokens";
import { useAnalyticsPreferences } from "../website/WebsiteAnalytics";
export function SiteFooter() {
  const { openPreferences } = useAnalyticsPreferences();
  return (
    <Box
      component="footer"
      style={{
        borderTop: `1px solid ${DARK.border}`,
        background: DARK.recessed,
      }}
      py="xl"
    >
      <Container size="lg">
        <Text size="xs" c={DARK.muted}>
          MIT-licensed. Self-hosted. Website analytics are optional.
        </Text>
        <Group mt="md" gap="lg">
          <Anchor component={Link} to="/about/" size="sm">
            About
          </Anchor>
          <Anchor component={Link} to="/privacy/" size="sm">
            Privacy
          </Anchor>
          <Button
            variant="transparent"
            size="compact-sm"
            onClick={openPreferences}
          >
            Analytics preferences
          </Button>
          <Anchor href={GITHUB_REPO} size="sm">
            GitHub
          </Anchor>
          <Anchor href="/llms.txt" size="sm">
            AI discovery file
          </Anchor>
        </Group>
      </Container>
    </Box>
  );
}
