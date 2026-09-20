import { Anchor, Box, Button, Container, Group, Stack, Text, Title } from "@mantine/core";
import { useDocumentTitle } from "@mantine/hooks";
import { Link } from "react-router-dom";
import { Bento, Tile } from "./Bento";
import { CtaBand } from "./CtaBand";
import { MarketingShell } from "./MarketingShell";
import { Section } from "./Section";
import { USE_CASES, USE_CASES_FOOTER, USE_CASES_INTRO } from "./useCasesCopy";
import { PAGE_HERO_CLASS, USE_CASE_REVEAL_CLASS } from "./marketingStyles";
import { DARK } from "../tokens";

export function UseCases() {
  useDocumentTitle("Use cases — Hookway");
  return (
    <MarketingShell>
      <Box component="section" className={PAGE_HERO_CLASS} pt={56} pb={64} style={{ background: DARK.bg }}>
        <Container size="lg">
          <Stack gap="md" maw={720}>
            <Text
              component="p"
              size="sm"
              fw={650}
              c={DARK.accent}
              style={{ letterSpacing: 0.2, margin: 0 }}
            >
              Hookway
            </Text>
            <Title
              order={1}
              c={DARK.text}
              style={{ fontSize: "clamp(32px, 5vw, 44px)", lineHeight: 1.15, letterSpacing: -0.4, fontWeight: 650 }}
            >
              {USE_CASES_INTRO.title}
            </Title>
            <Text size="lg" c={DARK.muted} maw={620}>
              {USE_CASES_INTRO.lead}
            </Text>
            <Group gap="sm" mt="xs">
              <Button component={Link} to="/builder" size="md" color="brand">
                Open the builder
              </Button>
              <Button
                component="a"
                href={USE_CASES_FOOTER.examplesHref}
                target="_blank"
                rel="noopener noreferrer"
                size="md"
                variant="default"
                styles={{ root: { background: DARK.surface, borderColor: DARK.border, color: DARK.text } }}
              >
                Browse examples
              </Button>
            </Group>
          </Stack>
        </Container>
      </Box>

      <Section
        title="Runnable patterns"
        lead="Each case links to a YAML example in the repository. If it is not in examples/, it is not claimed here."
        background={DARK.recessed}
      >
        <Bento>
          {USE_CASES.map((uc, index) => (
            <Tile key={uc.id} span={index === 0 ? 6 : 3}>
              <Stack gap={10} className={USE_CASE_REVEAL_CLASS} style={{ animationDelay: `${index * 40}ms` }}>
                <Text fw={650} c={DARK.text} size="md">
                  {uc.title}
                </Text>
                <Text size="sm" c={DARK.muted}>
                  {uc.summary}
                </Text>
                <Text size="sm" c={DARK.muted} style={{ lineHeight: 1.55 }}>
                  {uc.detail}
                </Text>
                <Anchor
                  href={uc.exampleHref}
                  target="_blank"
                  rel="noopener noreferrer"
                  size="sm"
                  c={DARK.accent}
                  style={{ fontFamily: "var(--mantine-font-family-monospace)" }}
                >
                  {uc.exampleLabel}
                </Anchor>
              </Stack>
            </Tile>
          ))}
        </Bento>
      </Section>

      <CtaBand />
    </MarketingShell>
  );
}
