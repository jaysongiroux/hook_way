import { Anchor, Box, Container, Group } from "@mantine/core";
import { Link } from "react-router-dom";
import { Logo } from "../brand/Logo";
import { HEADER_HEIGHT, HEADER_STYLE } from "../brand/chrome";
import { DOC_LINKS, GITHUB_REPO } from "./links";
import { DARK } from "../tokens";

const CONFIG_DOC = DOC_LINKS.find((d) => d.path === "docs/configuration.md")!;

export function SiteHeader() {
  return (
    <Box
      component="header"
      style={{
        ...HEADER_STYLE,
        position: "sticky",
        top: 0,
        zIndex: 10,
      }}
    >
      <Container size="lg">
        <Group h={HEADER_HEIGHT} justify="space-between">
          <Logo to="/" />
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
