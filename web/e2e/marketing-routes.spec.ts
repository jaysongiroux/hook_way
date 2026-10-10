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
  { path: "/", heading: /receive, verify, and route webhooks/i },
  { path: "/use-cases", heading: /Patterns that stay in your infrastructure/i },
  { path: "/compare", heading: /How Hookway compares/i },
];

for (const { path, heading } of ROUTES) {
  test(`${path} renders its heading`, async ({ page }) => {
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
    // A route that mounts but throws during render still leaves the H1 from a
    // previous paint in some setups, so confirm the page's own chrome is up.
    await expect(page.getByRole("navigation", { name: "Primary" })).toBeVisible();
  });
}

test("the marketing nav moves between the three public routes", async ({ page }) => {
  await page.goto("/");
  const nav = page.getByRole("navigation", { name: "Primary" });

  await nav.getByRole("link", { name: /use cases/i }).click();
  await expect(page).toHaveURL(/\/use-cases$/);
  await expect(page.getByRole("heading", { level: 1, name: /Patterns that stay in your infrastructure/i })).toBeVisible();

  await nav.getByRole("link", { name: /compare/i }).click();
  await expect(page).toHaveURL(/\/compare$/);
  await expect(page.getByRole("heading", { level: 1, name: /How Hookway compares/i })).toBeVisible();
});
