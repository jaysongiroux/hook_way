import { test, expect } from "@playwright/test";

// The rest of e2e/ only ever navigates to /builder, so the three public
// marketing routes had no browser coverage at all: a router misconfiguration,
// a crashing import, or a blank render on /, /use-cases or /compare would have
// been caught by nothing. The unit tests mount each page component directly,
// which cannot tell you whether the route resolves to it.
//
// Deliberately shallow. This asserts the route mounts and paints its own H1 --
// content assertions belong in the vitest specs next to the copy.
const ROUTES: { path: string; heading: RegExp }[] = [
  {
    path: "/",
    heading: /verify and route webhooks\. in your infrastructure/i,
  },
  { path: "/use-cases/", heading: /Patterns that stay in your infrastructure/i },
  { path: "/compare/", heading: /How Hookway compares/i },
];

for (const { path, heading } of ROUTES) {
  test(`${path} renders its heading`, async ({ page }) => {
    await page.goto(path);
    await expect(
      page.getByRole("heading", { level: 1, name: heading }),
    ).toBeVisible();
    // A route that mounts but throws during render still leaves the H1 from a
    // previous paint in some setups, so confirm the page's own chrome is up.
    await expect(
      page.getByRole("navigation", { name: "Primary" }),
    ).toBeVisible();
  });
}

test("the marketing nav moves between the three public routes", async ({
  page,
}) => {
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Primary" });

  await page.evaluate(() => window.scrollTo(0, 1800));
  await expect
    .poll(() => page.evaluate(() => window.scrollY))
    .toBeGreaterThan(0);
  await nav.getByRole("link", { name: /use cases/i }).click();
  await expect(page).toHaveURL(/\/use-cases\/$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(
    page.getByRole("heading", {
      level: 1,
      name: /Patterns that stay in your infrastructure/i,
    }),
  ).toBeVisible();

  await page.evaluate(() => window.scrollTo(0, 900));
  await nav.getByRole("link", { name: /compare/i }).click();
  await expect(page).toHaveURL(/\/compare\/$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  await expect(
    page.getByRole("heading", { level: 1, name: /How Hookway compares/i }),
  ).toBeVisible();
});

test("a direct marketing section link retains its target", async ({ page }) => {
  await page.goto("/#mcp-gateway");
  await expect(
    page.getByRole("heading", { name: "Give agents tools and events" }),
  ).toBeInViewport();
  await expect
    .poll(() => page.evaluate(() => window.scrollY))
    .toBeGreaterThan(0);
});

test("reduced motion keeps marketing content visible without entrance animations", async ({
  page,
}) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await page.getByRole("tab", { name: "Check & prepare" }).click();
  await expect(page.getByRole("tabpanel")).toHaveCSS("opacity", "1");
  expect(
    await page
      .locator("main")
      .evaluate(
        (main) =>
          main
            .getAnimations({ subtree: true })
            .filter((animation) =>
              Number.isFinite(Number(animation.effect?.getTiming().iterations)),
            ).length,
      ),
  ).toBe(0);
});

for (const width of [1440, 390]) {
  test(`MCP is discoverable and readable at ${width}px`, async ({
    page,
  }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto("/");
    await expect(page).toHaveTitle("Hookway | Self-hosted webhook gateway");
    await page
      .getByRole("link", { name: "authenticated MCP endpoints", exact: true })
      .click();
    await expect(page).toHaveURL(/#mcp-gateway$/);
    const section = page.locator("#mcp-gateway");
    await expect(
      section.getByRole("heading", {
        name: "Give agents tools and events",
      }),
    ).toBeVisible();
    await expect(section.getByRole("figure")).toBeVisible();
    await expect(
      section.getByText("issue.opened", { exact: true }),
    ).toBeVisible();
    await expect(
      section.getByText("get_issue", { exact: true }),
    ).toBeVisible();
    await expect(
      section.getByText("Signed callback → subscribed agent", { exact: true }),
    ).toBeVisible();
    await expect(
      section.getByRole("link", { name: "Import an API as MCP tools" }),
    ).toHaveAttribute("href", /openapi-import/);
    await expect
      .poll(() =>
        page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      )
      .toBe(true);
    await section.screenshot({
      path: testInfo.outputPath(`mcp-marketing-${width}.png`),
    });
    await expect(section.getByRole("link", { name: "Try the issue event demo" }))
      .toHaveAttribute("href", "/docs/examples/mcp-events/demo/");
    await page.goto("/compare/");
    const row = page
      .getByRole("rowheader", { name: /^MCP tools/ })
      .locator("..");
    await row.scrollIntoViewIfNeeded();
    await expect(row.locator('[data-product="hookway"]')).toContainText("Yes");
    await expect(row.locator('[data-product="hookdeck"]')).toContainText("Yes");
    await expect
      .poll(() =>
        page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
      )
      .toBe(true);
    expect(errors).toEqual([]);
  });
}
