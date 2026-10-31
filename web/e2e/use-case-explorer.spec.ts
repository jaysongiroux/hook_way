import { test, expect } from "@playwright/test";
import { SCENARIO_GROUPS } from "../src/marketing/useCaseDiagrams";

for (const width of [1440, 1024, 769, 390]) {
  test(`visual use cases preserve all runnable examples at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/use-cases/");
    for (const item of SCENARIO_GROUPS.flatMap((group) => group.items)) {
      const panel = page.locator(`article#${item.id}`);
      await expect(panel.getByRole("heading", { level: 3 })).toBeVisible();
      await expect(panel.locator('a[href^="/docs/examples/"]')).toBeVisible();
      await panel.getByText("How this pattern works", { exact: true }).click();
      await expect(panel.getByRole("img")).toBeVisible();
      // SVGs can fit the viewport while scaling their labels below readable
      // sizes. Include intermediate viewport widths in that check.
      await expect
        .poll(() =>
          panel.getByRole("img").evaluate((svg) =>
            Math.min(
              ...Array.from(
                svg.querySelectorAll<SVGTextElement>(
                  "[data-scenario-node] text:first-of-type",
                ),
                (text) =>
                  parseFloat(getComputedStyle(text).fontSize) *
                  (text.getScreenCTM()?.a ?? 0),
              ),
            ),
          ),
        )
        .toBeGreaterThanOrEqual(12);
      await expect(panel.locator('a[href^="/docs/examples/"]')).toHaveAttribute(
        "href",
        /^\/docs\/examples\//,
      );
      await expect(panel.locator("details p")).toBeVisible();
      await expect
        .poll(() =>
          page.evaluate(
            () => document.documentElement.scrollWidth <= innerWidth,
          ),
        )
        .toBe(true);
    }
  });
}

test("scenario details support keyboard access", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/use-cases/");
  const scenario = page.locator("article#mcp-events");
  const summary = scenario.locator("summary");
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(scenario.locator("details")).toHaveAttribute("open", "");
  await expect(scenario.getByRole("img")).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(scenario.locator("details")).not.toHaveAttribute("open", "");
});
