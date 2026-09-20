import { DARK } from "../tokens";

export const LIFT_CLASS = "hookway-lift";
export const BENTO_CLASS = "hookway-bento";
export const TILE_CLASS = "hookway-tile";
export const DESTINATION_ITEM_CLASS = "hookway-destination-item";
export const MARQUEE_SHELL_CLASS = "hookway-marquee-shell";
export const MARQUEE_VIEWPORT_CLASS = "hookway-marquee-viewport";
export const MARQUEE_TRACK_CLASS = "hookway-marquee-track";
export const MARQUEE_GROUP_CLASS = "hookway-marquee-group";
export const PAGE_HERO_CLASS = "hookway-page-hero";
export const USE_CASE_REVEAL_CLASS = "hookway-use-case-reveal";
export const COMPARE_TABLE_CLASS = "hookway-compare-table";
export const COMPARE_LEVEL_CLASS = "hookway-compare-level";

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
.${DESTINATION_ITEM_CLASS} {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  gap: 8px;
  min-height: 36px;
  padding: 7px 11px;
  color: ${DARK.text};
  background: ${DARK.bg};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
}
.${DESTINATION_ITEM_CLASS} svg {
  flex: 0 0 auto;
  color: ${DARK.accent};
}
.${MARQUEE_SHELL_CLASS} {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  align-items: center;
  gap: 32px;
}
.${MARQUEE_VIEWPORT_CLASS} {
  min-width: 0;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(to right, transparent, black 28px, black calc(100% - 28px), transparent);
  mask-image: linear-gradient(to right, transparent, black 28px, black calc(100% - 28px), transparent);
}
.${MARQUEE_TRACK_CLASS} {
  display: flex;
  width: max-content;
  will-change: transform;
  animation: hookway-destinations 42s linear infinite;
}
.${MARQUEE_TRACK_CLASS}[data-running="false"],
.${MARQUEE_VIEWPORT_CLASS}:hover .${MARQUEE_TRACK_CLASS} {
  animation-play-state: paused;
}
.${MARQUEE_GROUP_CLASS} {
  display: flex;
  flex: 0 0 auto;
  gap: 10px;
  padding-right: 10px;
}
@keyframes hookway-destinations {
  from { transform: translate3d(0, 0, 0); }
  to { transform: translate3d(-50%, 0, 0); }
}
.${PAGE_HERO_CLASS} {
  position: relative;
}
.${PAGE_HERO_CLASS}::after {
  content: "";
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 1px;
  background: linear-gradient(to right, transparent, ${DARK.border} 18%, ${DARK.border} 82%, transparent);
  pointer-events: none;
}
.${USE_CASE_REVEAL_CLASS} {
  animation: hookway-use-case-in 420ms ease both;
}
@keyframes hookway-use-case-in {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}
.${COMPARE_TABLE_CLASS} table {
  min-width: 720px;
}
.${COMPARE_TABLE_CLASS} tbody tr {
  transition: background-color 140ms ease;
}
.${COMPARE_TABLE_CLASS} tbody tr:hover {
  background: ${DARK.bg};
}
.${COMPARE_LEVEL_CLASS}[data-level="yes"] { color: ${DARK.accent}; }
.${COMPARE_LEVEL_CLASS}[data-level="partial"] { color: ${DARK.text}; }
.${COMPARE_LEVEL_CLASS}[data-level="no"],
.${COMPARE_LEVEL_CLASS}[data-level="n/a"] { color: ${DARK.muted}; }
.hookway-compare-products {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
@media (max-width: 62em) {
  .${MARQUEE_SHELL_CLASS} {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }
  .hookway-compare-products {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (prefers-reduced-motion: reduce) {
  .${LIFT_CLASS} { transition: none; }
  .${USE_CASE_REVEAL_CLASS} { animation: none; }
  .${COMPARE_TABLE_CLASS} tbody tr { transition: none; }
  .${MARQUEE_VIEWPORT_CLASS} {
    -webkit-mask-image: none;
    mask-image: none;
  }
  .${MARQUEE_TRACK_CLASS} {
    display: block;
    width: 100%;
    max-width: 100%;
    animation: none;
  }
  .${MARQUEE_GROUP_CLASS}:first-child {
    width: 100%;
    padding-right: 0;
    flex: 1 1 auto;
    flex-wrap: wrap;
  }
  .${MARQUEE_GROUP_CLASS}[aria-hidden="true"] { display: none; }
}
`;
