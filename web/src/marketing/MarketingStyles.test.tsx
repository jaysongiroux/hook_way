import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { MARKETING_CSS, LIFT_CLASS, MARQUEE_TRACK_CLASS, TILE_CLASS } from "./marketingStyles";
import { Marketing } from "./Marketing";

test("the hover lift is defined and switched off under reduced motion", () => {
  expect(MARKETING_CSS).toContain(`.${LIFT_CLASS}`);
  expect(MARKETING_CSS).toContain("prefers-reduced-motion");
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*transition:\s*none/);
});

test("page hero and use-case reveal motions respect reduced motion", () => {
  expect(MARKETING_CSS).toContain("hookway-page-hero");
  expect(MARKETING_CSS).toContain("hookway-use-case-reveal");
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*use-case-reveal[\s\S]*animation:\s*none/);
});

test("the destination marquee pauses when hidden or hovered and becomes static under reduced motion", () => {
  expect(MARKETING_CSS).toContain(`.${MARQUEE_TRACK_CLASS}[data-running="false"]`);
  expect(MARKETING_CSS).toMatch(/marquee-viewport:hover[\s\S]*animation-play-state:\s*paused/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-track[\s\S]*animation:\s*none/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-track[\s\S]*max-width:\s*100%/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*marquee-group:first-child[\s\S]*flex-wrap:\s*wrap/);
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*aria-hidden="true"[\s\S]*display:\s*none/);
});

test("every bento tile opts into the hover lift", () => {
  render(
    <MantineProvider theme={theme}>
      <MemoryRouter>
        <Marketing />
      </MemoryRouter>
    </MantineProvider>,
  );
  // Counting tiles would break every time the layout changes; the invariant
  // that matters is that no tile is left without the hover treatment.
  const main = screen.getByRole("main");
  const tiles = [...main.querySelectorAll(`.${TILE_CLASS}`)];
  expect(tiles.length).toBeGreaterThan(10);
  for (const t of tiles) {
    expect(t.classList.contains(LIFT_CLASS)).toBe(true);
  }
});
