import { expect, test } from "vitest";
import { DARK, EDGE, NODE, PORT, contrastRatio } from "./tokens";

// Contrast is computable, so compute it rather than trusting the eye. WCAG AA
// wants 4.5:1 for body text and 3:1 for large text and UI boundaries.
test("body and muted text are legible on the dark canvas", () => {
  expect(contrastRatio(DARK.text, DARK.bg)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(DARK.muted, DARK.bg)).toBeGreaterThanOrEqual(4.5);
  // Cards sit on a lifted surface, so text must clear that too.
  expect(contrastRatio(DARK.text, DARK.surface)).toBeGreaterThanOrEqual(4.5);
  expect(contrastRatio(DARK.muted, DARK.surface)).toBeGreaterThanOrEqual(4.5);
});

test("the accent is only trusted where its contrast allows", () => {
  // Large text and UI elements only: the accent does not clear 4.5 on navy.
  expect(contrastRatio(DARK.accent, DARK.bg)).toBeGreaterThanOrEqual(3);
  // A filled accent button carries white label text, which must clear AA.
  expect(contrastRatio("#ffffff", DARK.accent)).toBeGreaterThanOrEqual(4.5);
  // The brighter accent step exists for small text that has to be coloured.
  expect(contrastRatio(DARK.accentText, DARK.bg)).toBeGreaterThanOrEqual(4.5);
});

test("the contrast helper matches known reference pairs", () => {
  expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 1);
  expect(contrastRatio("#ffffff", "#ffffff")).toBeCloseTo(1, 5);
});

test("surfaces are distinguishable from the canvas and from each other", () => {
  expect(DARK.surface).not.toBe(DARK.bg);
  // A hairline border has to be visible without becoming a hard line.
  expect(contrastRatio(DARK.border, DARK.surface)).toBeGreaterThanOrEqual(1.2);
});

// The builder canvas paints node cards on DARK.surface. Every colour used on
// them is checked here, because a dark theme is exactly where "looks fine on
// my screen" goes wrong.
test("node identity borders are visible against the card surface", () => {
  for (const [kind, color] of Object.entries(NODE.border)) {
    // UI boundaries need 3:1.
    expect(contrastRatio(color, DARK.surface), `${kind} border`).toBeGreaterThanOrEqual(3);
  }
});

test("node label text clears AA on the card surface", () => {
  for (const [kind, color] of Object.entries(NODE.label)) {
    expect(contrastRatio(color, DARK.surface), `${kind} label`).toBeGreaterThanOrEqual(4.5);
  }
});

test("the destination type badge is legible on its own tint", () => {
  expect(contrastRatio(NODE.badgeText, NODE.badgeBg)).toBeGreaterThanOrEqual(4.5);
  // The tint must also separate from the card it sits on.
  expect(contrastRatio(NODE.badgeBg, DARK.surface)).toBeGreaterThanOrEqual(1.1);
});

test("port-type handles are visible against the card surface", () => {
  for (const [port, color] of Object.entries(PORT)) {
    expect(contrastRatio(color, DARK.surface), `${port} handle`).toBeGreaterThanOrEqual(3);
  }
});

test("edge lines are visible against the canvas", () => {
  // A thin line is a UI boundary: 3:1.
  expect(contrastRatio(EDGE.line, DARK.bg)).toBeGreaterThanOrEqual(3);
  expect(contrastRatio(EDGE.selected, DARK.bg)).toBeGreaterThanOrEqual(3);
  expect(contrastRatio(EDGE.labelBorder, DARK.surface)).toBeGreaterThanOrEqual(1.5);
});
