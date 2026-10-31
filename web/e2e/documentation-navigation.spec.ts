import { test, expect } from "@playwright/test";

for (const width of [1440, 390]) {
  test(`documentation source links and nested outline work at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto("/docs/");
    await expect(
      page.getByRole("heading", {
        level: 1,
        name: "Documentation",
        exact: true,
      }),
    ).toBeVisible();
    await expect(page.locator(".docs-breadcrumb")).toBeVisible();
    await expect(page.locator(".docs-breadcrumb").getByRole("link", { name: "Home", exact: true }))
      .toHaveAttribute("href", "/");
    await expect(
      page.getByRole("link", { name: "View on GitHub" }),
    ).toHaveAttribute(
      "href",
      "https://github.com/jaysongiroux/hook_way/blob/main/docs/README.md",
    );

    await page.goto("/docs/guides/mcp-gateway/");
    await page.locator(".docs-content").waitFor();
    const disclosure = page.locator(".docs-outline");
    if ((await disclosure.getAttribute("open")) === null)
      await disclosure.locator("summary").click();
    const outline = page.getByRole("navigation", {
      name: "On this page",
      exact: true,
    });
    const authentication = outline
      .locator("li")
      .filter({
        has: page.getByRole("link", {
          name: "Authentication and permissions",
          exact: true,
        }),
      })
      .first();
    await expect(
      authentication.locator("ul > li > a", { hasText: "OAuth configuration" }),
    ).toBeVisible();
    await expect(
      outline
        .locator("ul ul ul")
        .getByRole("link", {
          name: "Set up the identity provider",
          exact: true,
        }),
    ).toBeVisible();
    await page.screenshot({
      path: test.info().outputPath(`docs-outline-${width}.png`),
    });

    // Capture actual browser scroll positions, including frames between the
    // starting point and destination, rather than only spying on an API call.
    await page.evaluate(() => {
      const samples: number[] = [];
      const state = window as typeof window & { docsScrollSamples: number[] };
      state.docsScrollSamples = samples;
      window.addEventListener("scroll", () => samples.push(window.scrollY));
    });
    await outline
      .getByRole("link", { name: "Tool contracts and mapping", exact: true })
      .click();
    await expect(page).toHaveURL(/#tool-contracts-and-mapping$/);
    await expect
      .poll(() =>
        page
          .locator("#tool-contracts-and-mapping")
          .evaluate((el) => Math.round(el.getBoundingClientRect().top)),
      )
      .toBe(90);
    const samples = await page.evaluate(
      () =>
        (window as typeof window & { docsScrollSamples: number[] })
          .docsScrollSamples,
    );
    expect(samples.length).toBeGreaterThan(2);
    await expect(
      page.getByRole("heading", {
        name: "Tool contracts and mapping",
        exact: true,
      }),
    ).toBeInViewport();
  });
}

test("outline links respect reduced motion and can revisit the same heading", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/docs/guides/mcp-gateway/");
  await page.locator(".docs-content").waitFor();
  const disclosure = page.locator(".docs-outline");
  if ((await disclosure.getAttribute("open")) === null)
    await disclosure.locator("summary").click();
  const link = page
    .getByRole("navigation", { name: "On this page", exact: true })
    .getByRole("link", { name: "OAuth configuration", exact: true });
  await link.click();
  await expect(page).toHaveURL(/#oauth-configuration$/);
  await expect
    .poll(() =>
      page
        .locator("#oauth-configuration")
        .evaluate((el) => Math.round(el.getBoundingClientRect().top)),
    )
    .toBe(90);
  await page.locator(".docs-outline").scrollIntoViewIfNeeded();
  await link.click();
  await expect
    .poll(() =>
      page
        .locator("#oauth-configuration")
        .evaluate((el) => Math.round(el.getBoundingClientRect().top)),
    )
    .toBe(90);
});
