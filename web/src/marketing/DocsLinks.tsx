import { Stack, Text } from "@mantine/core";
import { Bento, Tile } from "./Bento";
import { DOC_LINKS, EXAMPLES } from "./links";
import { Section } from "./Section";
import { DARK } from "../tokens";

const CARDS = [...DOC_LINKS, EXAMPLES];

// The website renders the versioned repo sources; no documentation is duplicated.
export function DocsLinks() {
  return (
    <Section
      title="Documentation is versioned with the code"
      lead="Read configuration, CLI, security, operations, and examples here. Each page is built from the versioned repository source."
      background={DARK.recessed}
    >
      <Bento>
        {CARDS.map((c) => (
          <Tile key={c.path} span={2}>
            <a href={c.href} style={{ textDecoration: "none" }}>
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
