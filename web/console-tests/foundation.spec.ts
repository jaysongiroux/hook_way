import { expect, test } from "@playwright/test";

for (const width of [1440, 390]) {
  test(`built console loads real WASM at the product root at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const errors: string[] = [];
    const assets: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("request", (request) =>
      assets.push(new URL(request.url()).pathname),
    );
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: "Replay console" }),
    ).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            typeof (window as Window & { hookwayContractInfo?: () => string })
              .hookwayContractInfo,
        ),
      )
      .toBe("function");
    await expect(page.getByRole("status")).toHaveCount(0);
    await expect(page.getByRole("alert")).toHaveCount(0);
    expect(assets).toContain("/wasm_exec.js");
    expect(assets).toContain("/hookway.wasm");
    expect(assets.some((path) => path.startsWith("/v1/"))).toBe(false);
    expect(assets.some((path) => /(?:App(?:-|\.tsx)|graph\/(?:store|Canvas|Palette)|marketing)/.test(path))).toBe(
      false,
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect(errors).toEqual([]);
    await page.screenshot({
      path: testInfo.outputPath(`console-${width}.png`),
      fullPage: true,
    });
  });
}

test("missing WASM is visible and retry recovers", async ({ page }) => {
  await page.route("**/hookway.wasm", (route) =>
    route.fulfill({ status: 503, body: "Unavailable" }),
  );
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText(
    "Local validation is unavailable",
  );
  await page.unroute("**/hookway.wasm");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.getByRole("status")).toHaveCount(0);
});

test("real WASM validates resolution evidence without live API state", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Replay console" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => typeof window.hookwayReplayActionValidate)).toBe("function");
  const result = await page.evaluate(() => {
    const info = JSON.parse(window.hookwayContractInfo!());
    const validate = (note: string) => JSON.parse(window.hookwayReplayActionValidate!(JSON.stringify({ action: "resolve", idempotency_key: "receipt-42", note, reference: "INC-42" })));
    return { capabilities: info.capabilities, valid: validate("Confirmed receipt in destination audit"), invalid: validate(" \n "), nul: validate("receipt\u0000") };
  });
  expect(result.capabilities).toContain("replay_resolution_input");
  expect(result.valid.issues).toEqual([]);
  expect(result.invalid.issues).toEqual([expect.objectContaining({ field: "note" })]);
  expect(result.nul.issues).toEqual([expect.objectContaining({ field: "note" })]);
});
