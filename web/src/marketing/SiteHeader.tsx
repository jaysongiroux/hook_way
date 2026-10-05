import { Anchor, Box, Container, Group } from "@mantine/core";
import { Link, NavLink } from "react-router-dom";
import { Logo } from "../brand/Logo";
import { HEADER_HEIGHT, HEADER_STYLE } from "../brand/chrome";
import { DOC_LINKS, GITHUB_REPO } from "./links";
import {
  SITE_HEADER_INNER_CLASS,
  SITE_NAV_CLASS,
  SITE_NAV_SECONDARY_CLASS,
} from "./marketingStyles";
import { DARK } from "../tokens";

const CONFIG_DOC = DOC_LINKS.find((d) => d.path === "docs/reference/configuration.md")!;

const navLinkStyle = ({ isActive }: { isActive: boolean }) => ({
  color: isActive ? DARK.accent : DARK.muted,
  fontWeight: isActive ? 650 : 400,
  textDecoration: "none",
  fontSize: 14,
});

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
        <Group className={SITE_HEADER_INNER_CLASS} h={HEADER_HEIGHT} justify="space-between" wrap="nowrap">
          <Logo to="/" />
          <Group component="nav" aria-label="Primary" className={SITE_NAV_CLASS} gap="lg" wrap="nowrap">
            <NavLink to="/use-cases" style={navLinkStyle}>
              Use cases
            </NavLink>
            <NavLink to="/compare" style={navLinkStyle}>
              Compare
            </NavLink>
            <Anchor
              href={CONFIG_DOC.href}
              target="_blank"
              rel="noopener noreferrer"
              size="sm"
              c={DARK.muted}
            >
              Docs
            </Anchor>
            <Anchor
              className={SITE_NAV_SECONDARY_CLASS}
              href={GITHUB_REPO}
              target="_blank"
              rel="noopener noreferrer"
              size="sm"
              c={DARK.muted}
            >
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
