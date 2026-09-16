import { DARK } from "../tokens";

export const LIFT_CLASS = "hookway-lift";
export const BENTO_CLASS = "hookway-bento";
export const TILE_CLASS = "hookway-tile";

export const MARKETING_CSS = `
.${BENTO_CLASS} {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 16px;
}
.${TILE_CLASS} {
  background: ${DARK.surface};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  padding: 20px;
}
.${TILE_CLASS}[data-span="2"] { grid-column: span 2; }
.${TILE_CLASS}[data-span="3"] { grid-column: span 3; }
.${TILE_CLASS}[data-span="4"] { grid-column: span 4; }
.${TILE_CLASS}[data-span="6"] { grid-column: span 6; }
@media (max-width: 48em) {
  .${TILE_CLASS}[data-span] { grid-column: 1 / -1; }
}
.${LIFT_CLASS} {
  transition: border-color 140ms ease, background-color 140ms ease;
}
.${LIFT_CLASS}:hover {
  border-color: ${DARK.accent};
  background-color: ${DARK.surface};
}
@media (prefers-reduced-motion: reduce) {
  .${LIFT_CLASS} { transition: none; }
}
`;
