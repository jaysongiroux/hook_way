import { Group, Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { DOC_LINKS, EXAMPLES } from "./links";
import { Section } from "./Section";
import { DARK } from "../tokens";

const CARDS = [...DOC_LINKS, EXAMPLES];

// Links go straight to the copies on GitHub. Nothing here restates the docs;
// the site is a signpost so there is only ever one source of truth.
export function DocsLinks() {
  return (
    <Section
      eyebrow="Documentation"
      title="Read it before you install it"
      lead="Everything lives in the repository, versioned alongside the code."
      background={DARK.recessed}
    >
      <Bento>
        {CARDS.map((c) => (
          <Tile key={c.path} span={2}>
            <a href={c.href} target="_blank" rel="noopener noreferrer" style={{ textDecoration: "none" }}>
              <Stack gap={6}>
                <Group gap={6} wrap="nowrap">
                  <Text fw={600} c={DARK.accentText} size="sm">
                    {c.title}
                  </Text>
                  <Text c={DARK.accentText} size="sm">
                    ↗
                  </Text>
                </Group>
                <Text size="sm" c={DARK.muted}>
                  {c.description}
                </Text>
              </Stack>
            </a>
          </Tile>
        ))}
      </Bento>
    </Section>
  );
}
