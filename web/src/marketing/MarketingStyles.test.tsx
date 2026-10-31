import { expect, test } from "vitest";
import {
  EXPLORE_GRID_CLASS,
  LIFT_CLASS,
  MARKETING_CSS,
  MARQUEE_TRACK_CLASS,
} from "./marketingStyles";

test("the hover lift is defined and switched off under reduced motion", () => {
  expect(MARKETING_CSS).toContain(`.${LIFT_CLASS}`);
  expect(MARKETING_CSS).toContain("prefers-reduced-motion");
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*transition:\s*none/);
});

test("marketing navigation has compact breakpoints for narrow screens", () => {
  expect(MARKETING_CSS).toMatch(/max-width:\s*48em[\s\S]*site-nav[\s\S]*gap:\s*12px/);
  expect(MARKETING_CSS).toMatch(/max-width:\s*30em[\s\S]*hookway-logo-word[\s\S]*display:\s*none/);
});

test("the homepage exploration paths collapse to one column on narrow screens", () => {
  expect(MARKETING_CSS).toContain(`.${EXPLORE_GRID_CLASS}`);
  expect(MARKETING_CSS).toMatch(/max-width:\s*48em[\s\S]*explore-grid[\s\S]*grid-template-columns:\s*minmax\(0, 1fr\)/);
  expect(MARKETING_CSS).toMatch(/max-width:\s*48em[\s\S]*explore-secondary[\s\S]*border-left:\s*0/);
});

test("the destination marquee pauses when hidden or hovered and becomes static under reduced motion", () => {
  expect(MARKETING_CSS).toContain(`.${MARQUEE_TRACK_CLASS}[data-running="false"]`);
  expect(MARKETING_CSS).toMatch(/marquee-viewport:hover[\s\S]*animation-play-state:\s*paused/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-track[\s\S]*animation:\s*none/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-track[\s\S]*max-width:\s*100%/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-group:first-child[\s\S]*flex-wrap:\s*wrap/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*aria-hidden="true"[\s\S]*display:\s*none/);
});
