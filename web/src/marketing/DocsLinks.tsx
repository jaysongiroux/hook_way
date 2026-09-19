import { Stack, Text } from "@mantine/core";
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
      title="Documentation is versioned with the code"
      lead="Configuration, CLI, security, operations, and examples live in the repository."
      background={DARK.recessed}
    >
      <Bento>
        {CARDS.map((c) => (
          <Tile key={c.path} span={2}>
            <a href={c.href} target="_blank" rel="noopener noreferrer" style={{ textDecoration: "none" }}>
              <Stack gap={6}>
                <Text fw={600} c={DARK.accent} size="sm">
                  {c.title}
                </Text>
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
