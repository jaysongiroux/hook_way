import { DARK, EDGE } from "../tokens";

export const LIFT_CLASS = "hookway-lift";
export const DESTINATION_ITEM_CLASS = "hookway-destination-item";
export const MARQUEE_SHELL_CLASS = "hookway-marquee-shell";
export const MARQUEE_VIEWPORT_CLASS = "hookway-marquee-viewport";
export const MARQUEE_TRACK_CLASS = "hookway-marquee-track";
export const MARQUEE_GROUP_CLASS = "hookway-marquee-group";
export const PAGE_HERO_CLASS = "hookway-page-hero";
export const COMPARE_TABLE_CLASS = "hookway-compare-table";
export const COMPARE_LEVEL_CLASS = "hookway-compare-level";
export const SITE_HEADER_INNER_CLASS = "hookway-site-header-inner";
export const SITE_NAV_CLASS = "hookway-site-nav";
export const SITE_NAV_SECONDARY_CLASS = "hookway-site-nav-secondary";
export const EXPLORE_GRID_CLASS = "hookway-explore-grid";
export const EXPLORE_PRIMARY_CLASS = "hookway-explore-primary";
export const EXPLORE_SECONDARY_CLASS = "hookway-explore-secondary";

export const MARKETING_CSS = `
.hookway-site-shell { --hookway-content-width: 80rem; }
.hookway-site-shell .mantine-Container-root { max-width: var(--hookway-content-width); }
.hookway-marketing-page main a { text-underline-offset: 4px; transition: color 140ms ease; }
.hookway-marketing-page .mantine-Button-root { transition: background-color 140ms ease, border-color 140ms ease; }

.hookway-overview-hero {
  padding: 72px 0 48px;
}
.hookway-hero-intro {
  display: grid;
  grid-template-columns: 1.15fr 1fr;
  gap: 64px;
  align-items: start;
  margin-bottom: 40px;
}
.hookway-hero-title {
  font-size: clamp(36px, 4.2vw, 58px);
  font-weight: 650;
  line-height: 1.1;
  letter-spacing: -0.03em;
  text-wrap: balance;
}
.hookway-hero-lead {
  font-size: 20px;
  line-height: 1.6;
  max-width: 48ch;
}
.hookway-hero-caption {
  margin-top: 16px;
  max-width: 76ch;
  line-height: 1.5;
}
.hookway-hero-canvas { overflow-x: auto; }
.hookway-flow-scroll-hint { display: none; }
/* Product explanations share the site's restrained surfaces and type. */
.hookway-setup-steps { margin: 0; padding-left: 24px; display: grid; gap: 24px; }
.hookway-setup-steps li { padding-left: 8px; }
.hookway-setup-steps li::marker { color: ${DARK.accent}; font-weight: 600; }
.hookway-mcp-example { padding: 24px; background: ${DARK.surface}; border: 1px solid ${DARK.border}; border-radius: 4px; min-width: 0; }
.hookway-mcp-question { padding: 20px 0; border-bottom: 1px solid ${DARK.border}; }
.hookway-tool-path { list-style: none; padding: 0 0 0 16px; margin: 24px 0; border-left: 1px solid ${DARK.border}; display: grid; gap: 24px; }
.hookway-tool-path li { display: grid; gap: 6px; position: relative; padding-left: 8px; }
.hookway-tool-path li::before { content: ""; position: absolute; left: -21px; top: 8px; width: 8px; height: 8px; border: 1px solid ${DARK.accent}; background: ${DARK.surface}; }
.hookway-tool-path code { color: ${DARK.accent}; font-size: 13px; overflow-wrap: anywhere; }
.hookway-section-note { margin-top: 32px; padding-top: 24px; border-top: 1px solid ${DARK.border}; }
.hookway-section-note > p { max-width: 80ch; }
.hookway-feature-tabs .mantine-Tabs-tab { min-height: 48px; color: ${DARK.muted}; font-weight: 600; }
.hookway-feature-tabs .mantine-Tabs-tab[data-active] { color: ${DARK.accent}; }
.hookway-capability-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); gap: 32px; padding: 24px 0; border-bottom: 1px solid ${DARK.border}; }
.hookway-yaml-example { display: grid; grid-template-columns: minmax(0, 1.65fr) minmax(0, 1fr); gap: 32px; align-items: start; }
.hookway-yaml-source { min-width: 0; }
.hookway-yaml-flow { min-width: 0; padding: 16px 0; }
.hookway-node-path { list-style: none; padding: 0; margin: 24px 0; }
.hookway-node-path li { display: flex; gap: 16px; align-items: start; position: relative; padding-bottom: 24px; }
.hookway-node-path li:not(:last-child)::after { content: ""; position: absolute; top: 32px; bottom: 0; left: 24px; border-left: 1px solid ${DARK.border}; }
.hookway-node-path li > code { position: relative; z-index: 1; flex: 0 0 48px; text-align: center; padding: 6px 0; font-size: 13px; color: ${DARK.accent}; border: 1px solid ${DARK.border}; border-radius: 4px; background: ${DARK.surface}; }
.hookway-node-path li > div { min-width: 0; overflow-wrap: anywhere; }
.hookway-benchmark { border-bottom: 1px solid ${DARK.border}; padding-bottom: 24px; }
.hookway-benchmark-results { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 32px; margin: 32px 0 24px; }
.hookway-benchmark-value { font-size: clamp(22px, 2.5vw, 28px); line-height: 1.25; font-weight: 600; font-variant-numeric: tabular-nums; margin: 8px 0 12px; color: ${DARK.accent}; }
.hookway-details { border-top: 1px solid ${DARK.border}; border-bottom: 1px solid ${DARK.border}; }
.hookway-details summary { cursor: pointer; padding: 20px 0; color: ${DARK.accent}; font-weight: 600; }
.hookway-details summary:hover { text-decoration: underline; text-underline-offset: 4px; }
.hookway-details summary:focus-visible, .hookway-yaml-source [tabindex]:focus-visible { outline: 2px solid ${DARK.accent}; outline-offset: 4px; }
.hookway-defaults { margin: 0; }
.hookway-defaults .hookway-capability-row:last-child { border-bottom: 0; }
.hookway-replay-steps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 32px; list-style: none; counter-reset: recovery; margin: 32px 0 0; padding: 0; }
.hookway-replay-steps li { counter-increment: recovery; border-top: 1px solid ${DARK.border}; padding-top: 16px; }
.hookway-replay-steps li::before { content: counter(recovery); display: block; color: ${DARK.accent}; font-size: 14px; font-weight: 600; margin-bottom: 8px; }
.hookway-docs-layout { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); gap: 48px; }
.hookway-docs-references { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 32px; }
.hookway-docs-start { align-self: start; }
.hookway-docs-start a { padding: 6px 0; }

.hookway-docs-list { list-style: none; margin: 12px 0 0; padding: 0; }
.hookway-docs-list li { padding: 12px 0; border-bottom: 1px solid ${DARK.border}; }
.hookway-docs-list a { display: inline-block; padding: 4px 0; font-weight: 500; }
.hookway-docs-footer { margin-top: 32px; padding-top: 24px; border-top: 1px solid ${DARK.border}; display: flex; flex-wrap: wrap; gap: 16px 32px; align-items: center; }
@media (max-width: 62em) {
  .hookway-yaml-example { grid-template-columns: minmax(0, 1fr); }
  .hookway-docs-layout { gap: 32px; }
  .hookway-docs-references { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 48em) {
  .hookway-capability-row, .hookway-docs-layout, .hookway-benchmark-results, .hookway-replay-steps { grid-template-columns: minmax(0, 1fr); }
  .hookway-capability-row { gap: 8px; padding: 20px 0; }
  .hookway-feature-tabs .mantine-Tabs-tab { padding: 12px 8px; font-size: 13px; }
  .hookway-mcp-example { padding: 20px; }
  .hookway-benchmark-results { gap: 24px; }
  .hookway-replay-steps { gap: 24px; }
}
.hookway-hero-canvas:focus-visible {
  outline: 2px solid ${DARK.accent};
  outline-offset: -2px;
}
.hookway-flow-event {
  opacity: 0;
  pointer-events: none;
  animation: hookway-flow-send 5s linear infinite;
  animation-delay: var(--flow-delay, 0s);
}
.hookway-hero-diagram[data-running="false"] .hookway-flow-event {
  animation-play-state: paused;
}
@keyframes hookway-flow-send {
  0% { stroke-dashoffset: 0.07; opacity: 0; }
  8% { opacity: 1; }
  44% { stroke-dashoffset: -1; opacity: 1; }
  50%, 100% { stroke-dashoffset: -1; opacity: 0; }
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
  grid-template-columns: minmax(0, 1fr);
  gap: 24px;
}
.hookway-marquee-heading {
  display: flex;
  justify-content: space-between;
  align-items: end;
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
.${COMPARE_TABLE_CLASS} table {
  min-width: 980px;
  border-collapse: separate;
  border-spacing: 0;
}
.${COMPARE_TABLE_CLASS} thead th,
.${COMPARE_TABLE_CLASS} tbody th {
  background: ${DARK.bg};
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
@media (max-width: 48em) {
  .${COMPARE_TABLE_CLASS} :is(th, td):first-child {
    position: static;
  }
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
.hookway-compare-differs {
  background: ${DARK.surface};
  border: 1px solid ${DARK.border};
  border-radius: 4px;
  padding: 20px 22px;
  margin-bottom: 20px;
}
.hookway-compare-differs-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
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
  .hookway-hero-intro { gap: 32px; }
  .hookway-compare-products {
    grid-template-columns: minmax(0, 1fr);
  }
  .hookway-compare-differs-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 48em) {
  .hookway-overview-hero { padding: 40px 0 32px; }
  .hookway-hero-intro { grid-template-columns: minmax(0, 1fr); gap: 24px; margin-bottom: 32px; }
  .hookway-hero-title { font-size: clamp(34px, 6vw, 48px); }
  .hookway-hero-lead { font-size: 18px; }
  .hookway-hero-canvas svg { min-width: 780px; }
  .hookway-flow-scroll-hint { display: block; border-top: 1px solid ${DARK.border}; }
  .hookway-marquee-heading { flex-direction: column; align-items: start; gap: 12px; }
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
  .hookway-flow-event { animation: none; display: none; }
  .hookway-flow-motion-control { display: none; }
  .${LIFT_CLASS} { transition: none; }
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
