import type { CSSProperties } from "react";
import { DARK } from "./tokens";

export const HEADER_HEIGHT = 60;

export const HEADER_STYLE: CSSProperties = {
  position: "relative",
  zIndex: 50,
  borderBottom: `1px solid ${DARK.border}`,
  background: DARK.surface,
};

export const TOOLBAR_DIVIDER: CSSProperties = {
  width: 1,
  height: 20,
  background: DARK.border,
  flexShrink: 0,
};
