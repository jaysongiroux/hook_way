import { test, expect } from "@playwright/test";
import { SCENARIO_GROUPS } from "../src/marketing/useCaseDiagrams";

for (const width of [1440, 1024, 769, 390]) {
  test(`visual use cases preserve all nine examples at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/use-cases");
    const panel = page.getByRole("tabpanel");
    for (const item of SCENARIO_GROUPS.flatMap((group) => group.items)) {
      if (width > 768)
        await page.getByRole("tab", { name: item.label, exact: true }).click();
      else
        await page
          .getByRole("combobox", { name: "Choose a use case" })
          .selectOption(item.id);
      await expect(panel.getByRole("heading", { level: 2 })).toBeVisible();
      await expect(panel.getByRole("img")).toBeVisible();
      // SVGs can fit the viewport while scaling their labels below readable
      // sizes. Include the sidebar's intermediate widths in that check.
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
      await expect(panel.getByRole("link")).toHaveAttribute(
        "href",
        /^\/docs\/examples\//,
      );
      await panel.getByText("How this pattern works", { exact: true }).click();
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

test("the scenario rail supports keyboard selection", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("/use-cases");
  const first = page.getByRole("tab", {
    name: "APIs for AI assistants",
    exact: true,
  });
  await first.focus();
  await page.keyboard.press("ArrowDown");
  await expect(
    page.getByRole("tab", { name: "Synchronous API proxy", exact: true }),
  ).toBeFocused();
  await expect(
    page.getByRole("tabpanel").getByRole("heading", { level: 2 }),
  ).toHaveText(/synchronous reverse proxy/);
});
