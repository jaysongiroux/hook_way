import { Box, Button, Container, Group, Stack, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import { QuickStart } from "./QuickStart";
import { DARK } from "../tokens";

export function CtaBand() {
  return (
    <Box
      component="section"
      py={72}
      style={{
        background: DARK.recessed,
        borderTop: `1px solid ${DARK.border}`,
      }}
    >
      <Container size="lg">
        <Stack align="center" gap="md">
          <Title order={2} c={DARK.text} ta="center" style={{ fontSize: 32, letterSpacing: -0.5, fontWeight: 650 }}>
            Run it yourself in a minute
          </Title>
          <Text size="md" c={DARK.muted} ta="center" maw={560}>
            Clone the repo and bring up the example stack. Nothing to sign up for, and no account to create.
          </Text>
          <Group mt="xs">
            <Button component={Link} to="/builder" size="md" color="brand">
              Build a config
            </Button>
          </Group>
        </Stack>
        <Box mt={36}>
          <QuickStart />
        </Box>
      </Container>
    </Box>
  );
}
