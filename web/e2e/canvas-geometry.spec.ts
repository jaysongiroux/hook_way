import { test, expect } from "@playwright/test";
import { BuilderPage } from "./builder";
import { EDGE, NODE_SIZE } from "../src/tokens";

// The unit test in RouteEdge.test.tsx can only check that the path stops
// EDGE.arrowGap short of the connection point React Flow reports. It cannot
// check where that lands on screen, because jsdom lays out no handles. That
// blind spot let the visible gap sit at 9px while the arithmetic looked right,
// so the gap is measured here in a real browser instead.
// A literal, not EDGE.arrowGap. Asserting against the token that drives the
// render is a tautology: raising the token raises the budget with it and the
// test keeps passing. This is the design requirement, stated independently.
const MAX_VISIBLE_GAP = 1;

test("every arrowhead stops within the clearance of its target port dot", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await expect(page.locator(".react-flow__edge-path").first()).toBeVisible();

  const gaps = await page.evaluate((dotDiameter: number) => {
    const dots = [...document.querySelectorAll(".react-flow__handle.target")].map((dot) => {
      const r = dot.getBoundingClientRect();
      return { nearEdgeX: r.left, centreX: r.left + r.width / 2, centreY: r.top + r.height / 2, width: r.width };
    });

    return [...document.querySelectorAll<SVGPathElement>(".react-flow__edge-path")].map((path) => {
      // The arrowhead's tip is drawn on the path's end point, so the end point
      // is where the arrow visibly points.
      const pt = path.getPointAtLength(path.getTotalLength());
      const m = path.getScreenCTM()!;
      const tipX = pt.x * m.a + pt.y * m.c + m.e;
      const tipY = pt.x * m.b + pt.y * m.d + m.f;

      // The nearest target dot is the one this edge terminates on.
      let best = dots[0];
      let bestDist = Infinity;
      for (const dot of dots) {
        const dist = Math.hypot(dot.centreX - tipX, dot.centreY - tipY);
        if (dist < bestDist) {
          bestDist = dist;
          best = dot;
        }
      }

      // The canvas is zoomed to fit, so divide the zoom back out to get canvas
      // units -- the units EDGE.arrowGap is expressed in. The dot's on-screen
      // diameter against its known canvas diameter recovers that zoom.
      const scale = best.width / dotDiameter;
      return (best.nearEdgeX - tipX) / scale;
    });
  }, NODE_SIZE.handle);

  expect(gaps.length).toBeGreaterThan(3);
  for (const gap of gaps) {
    expect(gap).toBeLessThanOrEqual(MAX_VISIBLE_GAP);
    // A gap of zero or less means the arrow is drawn over the dot and vanishes.
    expect(gap).toBeGreaterThan(0);
    // Separately, the rendered gap should be the token 1:1. If this drifts, the
    // comment on EDGE.arrowGap explaining why is out of date.
    expect(gap).toBeCloseTo(EDGE.arrowGap, 1);
  }
});
