import { Box } from "@mantine/core";
import type { ReactNode } from "react";
import { BENTO_CLASS, LIFT_CLASS, TILE_CLASS } from "./marketingStyles";

// Bento lays tiles on a six-column grid so tiles can differ in width. Uniform
// grids were what made the page monotonous; varying the spans gives the eye
// somewhere to land. Spans live in CSS (see marketingStyles) rather than inline
// so the mobile rule can override them.
export function Bento({ children }: { children: ReactNode }) {
  return <Box className={BENTO_CLASS}>{children}</Box>;
}

/** span is the tile width in grid columns out of six; tiles stack on mobile. */
export function Tile({ span, children }: { span: 2 | 3 | 4 | 6; children: ReactNode }) {
  return (
    <Box className={`${TILE_CLASS} ${LIFT_CLASS}`} data-span={span}>
      {children}
    </Box>
  );
}
