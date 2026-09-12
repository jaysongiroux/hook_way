import { DARK } from "../tokens";

// Shared CSS for the marketing page. One string, mounted once, because hover
// and motion need real rules that inline styles cannot express.

export const LIFT_CLASS = "hookway-lift";
export const PULSE_CLASS = "hookway-pulse";
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
  border-radius: 12px;
  padding: 20px;
}
.${TILE_CLASS}[data-span="2"] { grid-column: span 2; }
.${TILE_CLASS}[data-span="3"] { grid-column: span 3; }
.${TILE_CLASS}[data-span="4"] { grid-column: span 4; }
.${TILE_CLASS}[data-span="6"] { grid-column: span 6; }
/* Below the tablet breakpoint every tile takes the full row. The selector
   matches the span rules above so it wins on source order, not specificity. */
@media (max-width: 48em) {
  .${TILE_CLASS}[data-span] { grid-column: 1 / -1; }
}
.${LIFT_CLASS} {
  transition: transform 140ms ease, border-color 140ms ease, background-color 140ms ease;
}
.${LIFT_CLASS}:hover {
  transform: translateY(-2px);
  border-color: ${DARK.accent};
  background-color: #1F2E4A;
}
@keyframes hookway-pulse {
  from { stroke-dashoffset: 240; }
  to { stroke-dashoffset: 0; }
}
.${PULSE_CLASS} {
  stroke-dasharray: 14 226;
  animation: hookway-pulse 3s linear infinite;
}
@media (prefers-reduced-motion: reduce) {
  .${LIFT_CLASS} { transition: none; }
  .${LIFT_CLASS}:hover { transform: none; }
  .${PULSE_CLASS} { animation: none; opacity: 0; }
}
`;
