import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { MemoryRouter } from "react-router-dom";
import { theme } from "../theme";
import { MARKETING_CSS, LIFT_CLASS, TILE_CLASS } from "./marketingStyles";
import { Marketing } from "./Marketing";

test("the hover lift is defined and switched off under reduced motion", () => {
  expect(MARKETING_CSS).toContain(`.${LIFT_CLASS}`);
  expect(MARKETING_CSS).toContain("prefers-reduced-motion");
  expect(MARKETING_CSS).toMatch(/prefers-reduced-motion[\s\S]*transition:\s*none/);
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
