import { expect, test } from "@playwright/test";

for (const [label, width, height] of [["desktop", 1440, 1000], ["mobile", 390, 844]] as const) {
  test(`record marketing remains accurate and usable on ${label}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto("/");
    const replay = page.locator("section").filter({ has: page.getByRole("heading", { name: "Recover missed deliveries with the replay console", exact: true }) });
    await replay.scrollIntoViewIfNeeded();
    await expect(replay).toContainText("Inspections are view only");
    await expect(replay).toContainText("download captured files");
    await expect(replay.getByRole("link", { name: "See the sampled inspection example" })).toHaveAttribute("href", "/docs/examples/inspect/");
    const preview = replay.getByRole("img");
    await expect(preview).toBeVisible();
    await expect.poll(() => preview.evaluate((image) => (image as HTMLImageElement).complete && (image as HTMLImageElement).naturalWidth === 1280)).toBe(true);
    // Exclude the sticky global navigation from this section-only evidence.
    await replay.screenshot({ path: testInfo.outputPath(`records-marketing-replay-${label}.png`), animations: "disabled", style: "header { visibility: hidden !important; }" });
    await page.goto("/use-cases/");
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    const inspection = page.locator("article#sampled-inspection");
    await inspection.scrollIntoViewIfNeeded();
    await inspection.getByText("How this pattern works", { exact: true }).click();
    await expect(page.getByRole("link", { name: "inspect.yaml", exact: true })).toBeVisible();
    await expect(page.getByText("Spot-check webhooks while delivering every event", { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`records-marketing-usecases-${label}.png`), fullPage: true, animations: "disabled" });
    await page.goto("/compare/");
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByRole("main")).toContainText("Sample → Inspect adds view-only webhook observations");
    await page.screenshot({ path: testInfo.outputPath(`records-marketing-compare-${label}.png`), fullPage: true, animations: "disabled" });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(errors).toEqual([]);
  });
}
