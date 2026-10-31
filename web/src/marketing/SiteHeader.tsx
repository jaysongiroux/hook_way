import { Anchor, Box, Container, Group } from "@mantine/core";
import { Link, NavLink } from "react-router-dom";
import { Logo } from "../brand/Logo";
import { HEADER_HEIGHT, HEADER_STYLE } from "../brand/chrome";
import { docsHref } from "./links";
import { SITE_HEADER_INNER_CLASS, SITE_NAV_CLASS } from "./marketingStyles";
import { DARK } from "../tokens";

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
        <Group
          className={SITE_HEADER_INNER_CLASS}
          h={HEADER_HEIGHT}
          justify="space-between"
          wrap="nowrap"
        >
          <Logo to="/" />
          <Group
            component="nav"
            aria-label="Primary"
            className={SITE_NAV_CLASS}
            gap="lg"
            wrap="nowrap"
          >
            <NavLink to="/use-cases/" style={navLinkStyle}>
              Use cases
            </NavLink>
            <NavLink to="/mcp-gateway/" style={navLinkStyle}>
              MCP gateway
            </NavLink>
            <NavLink to="/compare/" style={navLinkStyle}>
              Compare
            </NavLink>
            <NavLink to={docsHref("docs/README.md")} style={navLinkStyle}>
              Docs
            </NavLink>
            <Anchor
              component={Link}
              to="/builder"
              size="sm"
              fw={600}
              c={DARK.accentText}
            >
              Builder
            </Anchor>
          </Group>
        </Group>
      </Container>
    </Box>
  );
}
