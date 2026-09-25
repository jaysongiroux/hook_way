import { DARK, EDGE } from "../tokens";

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
export const SITE_HEADER_INNER_CLASS = "hookway-site-header-inner";
export const SITE_NAV_CLASS = "hookway-site-nav";
export const SITE_NAV_SECONDARY_CLASS = "hookway-site-nav-secondary";
export const EXPLORE_GRID_CLASS = "hookway-explore-grid";
export const EXPLORE_PRIMARY_CLASS = "hookway-explore-primary";
export const EXPLORE_SECONDARY_CLASS = "hookway-explore-secondary";

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
  min-width: 980px;
  border-collapse: separate;
  border-spacing: 0;
}
.${COMPARE_TABLE_CLASS} :is(th, td):first-child {
  position: sticky;
  left: 0;
  z-index: 2;
  min-width: 210px;
  max-width: 260px;
  background: ${DARK.bg};
  box-shadow: inset -1px 0 0 ${DARK.border};
}
.${COMPARE_TABLE_CLASS} thead :is(th, td):first-child {
  z-index: 3;
}
.${COMPARE_TABLE_CLASS} :is(th, td):not(:first-child) {
  min-width: 240px;
}
.${COMPARE_TABLE_CLASS} :is(th, td)[data-product="hookway"] {
  background: #EEF3F8;
  box-shadow: inset 3px 0 0 ${DARK.accent};
}
.${COMPARE_TABLE_CLASS} thead th[data-product="hookway"] {
  background: #E4EDF6;
}
.${COMPARE_TABLE_CLASS}:focus-visible {
  outline: 2px solid ${DARK.accent};
  outline-offset: 2px;
}
.${COMPARE_TABLE_CLASS} tbody tr {
  transition: background-color 140ms ease;
}
.${COMPARE_TABLE_CLASS} tbody tr:hover {
  background: ${DARK.bg};
}
.${COMPARE_TABLE_CLASS} tbody tr:hover :is(th, td):first-child {
  background: ${DARK.bg};
}
.${COMPARE_TABLE_CLASS} tbody tr:hover :is(th, td)[data-product="hookway"] {
  background: #E4EDF6;
}
.${COMPARE_LEVEL_CLASS} {
  display: inline-block;
  padding: 1px 8px;
  border: 1px solid ${EDGE.line};
  border-radius: 2px;
  line-height: 1.5;
  letter-spacing: 0.01em;
  white-space: nowrap;
}
.${COMPARE_LEVEL_CLASS}[data-level="yes"] {
  color: ${DARK.accent};
  background: #E7EEF5;
}
.${COMPARE_LEVEL_CLASS}[data-level="partial"] {
  color: ${DARK.text};
  background: ${DARK.recessed};
}
.${COMPARE_LEVEL_CLASS}[data-level="no"],
.${COMPARE_LEVEL_CLASS}[data-level="n/a"] {
  color: ${DARK.muted};
  background: ${DARK.bg};
}
.hookway-compare-product[data-product="hookway"] {
  box-shadow: inset 3px 0 0 ${DARK.accent};
}
.hookway-compare-mark {
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.hookway-compare-differs {
  background: ${DARK.surface};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  padding: 20px 22px;
  margin-bottom: 20px;
}
.hookway-compare-differs-list {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
}
.${EXPLORE_GRID_CLASS} {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(260px, 1fr);
  border-top: 1px solid ${DARK.border};
  border-bottom: 1px solid ${DARK.border};
}
.${EXPLORE_PRIMARY_CLASS} {
  padding: 28px 40px 28px 0;
}
.${EXPLORE_SECONDARY_CLASS} {
  padding: 28px 0 28px 40px;
  border-left: 1px solid ${DARK.border};
}
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
  .hookway-compare-differs-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 48em) {
  .${EXPLORE_GRID_CLASS} {
    grid-template-columns: minmax(0, 1fr);
  }
  .${EXPLORE_PRIMARY_CLASS} {
    padding: 24px 0;
  }
  .${EXPLORE_SECONDARY_CLASS} {
    padding: 24px 0;
    border-top: 1px solid ${DARK.border};
    border-left: 0;
  }
  .${SITE_NAV_SECONDARY_CLASS} { display: none; }
  .${SITE_HEADER_INNER_CLASS},
  .${SITE_NAV_CLASS} { gap: 12px; }
}
@media (max-width: 30em) {
  .${SITE_HEADER_INNER_CLASS},
  .${SITE_NAV_CLASS} { gap: 8px; }
  .${SITE_HEADER_INNER_CLASS} .hookway-logo-word { display: none; }
  .${SITE_NAV_CLASS} a { font-size: 13px; }
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
