import { Box, Group, Text } from "@mantine/core";
import { Link } from "react-router-dom";
import { DARK } from "../tokens";

/** Mark size used in both navbars so the product identity does not shift. */
const LOGO_MARK_SIZE = 28;

export function LogoMark({ size = LOGO_MARK_SIZE }: { size?: number }) {
  return (
    <Box
      component="img"
      src="/hookway-icon-512.png"
      alt=""
      aria-hidden="true"
      w={size}
      h={size}
      style={{ display: "block", flexShrink: 0 }}
    />
  );
}

export function Logo({ to }: { to?: string }) {
  const inner = (
    <Group gap={10} wrap="nowrap">
      <LogoMark />
      <Text
        className="hookway-logo-word"
        fw={700}
        c={DARK.text}
        size="md"
        style={{ letterSpacing: -0.3, lineHeight: 1 }}
      >
        Hookway
      </Text>
    </Group>
  );
  if (!to) return inner;
  return (
    <Link
      to={to}
      aria-label="Hookway home"
      style={{ textDecoration: "none", color: "inherit", display: "inline-flex" }}
    >
      {inner}
    </Link>
  );
}
