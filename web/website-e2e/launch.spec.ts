import { expect, test, type Page, type Request } from "@playwright/test";
import { gunzipSync } from "node:zlib";
import registry from "../site.json" with { type: "json" };

type Event = { event: string; properties: Record<string, unknown> };
async function interceptAnalytics(page: Page, status = 200) {
  // Emulate a consented human visitor. The SDK intentionally excludes
  // navigator.webdriver sessions, including Playwright's default context.
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "webdriver", { get: () => false });
    Object.defineProperty(navigator, "userAgentData", { get: () => undefined });
    Object.defineProperty(navigator, "userAgent", {
      get: () =>
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36",
    });
  });
  const requests: Request[] = [];
  const events: Event[] = [];
  await page.route("https://ingest.example.com/**", async (route) => {
    const request = route.request();
    requests.push(request);
    const bytes = request.postDataBuffer();
    if (bytes) {
      let text = bytes.toString();
      if (new URL(request.url()).searchParams.get("compression") === "gzip-js")
        text = gunzipSync(bytes).toString();
      const encoded = new URLSearchParams(text).get("data");
      if (encoded)
        text =
          encoded.startsWith("{") || encoded.startsWith("[")
            ? encoded
            : Buffer.from(encoded, "base64").toString();
      try {
        const data = JSON.parse(text);
        for (const event of Array.isArray(data) ? data : [data])
          if (event.event) events.push(event);
      } catch {
        /* Flags/configuration requests are counted separately. */
      }
    }
    await route.fulfill({
      status,
      contentType: "application/json",
      body: "{}",
      headers: {
        "Access-Control-Allow-Origin": "*",
        "Access-Control-Allow-Headers": "Content-Type",
        "Access-Control-Allow-Methods": "POST, OPTIONS",
      },
    });
  });
  return { requests, events };
}

test("initial HTML exposes content, unique metadata and real error responses", async ({
  request,
}) => {
  const titles = new Set<string>();
  for (const path of [
    ...registry.pages.map((page) => page.path),
    ...registry.requiredDocs,
  ]) {
    const response = await request.get(path);
    expect(response.status(), path).toBe(200);
    const html = await response.text();
    expect(html.match(/<h1(?:\s[^>]*)?>/g), path).toHaveLength(1);
    expect(html, path).toContain(`href="https://www.gethookway.com${path}"`);
    expect(html, path).toContain('aria-label="Primary"');
    const title = html.match(/<title>(.*?)<\/title>/)![1];
    expect(titles.has(title), path).toBe(false);
    titles.add(title);
  }
  for (const path of ["/missing-launch-page/", "/docs/missing-launch-page/"]) {
    const response = await request.get(path);
    expect(response.status()).toBe(404);
    expect(await response.text()).toContain("noindex");
  }
  expect(await (await request.get("/builder")).text()).toContain("noindex");
  const sitemap = await (await request.get("/sitemap.xml")).text();
  expect(sitemap).not.toContain("/builder");
  expect(sitemap).not.toContain("<lastmod>");
  for (const path of registry.requiredDocs)
    expect(sitemap).toContain(registry.origin + path);
  expect(await (await request.get("/robots.txt")).text()).toContain(
    registry.origin + "/sitemap.xml",
  );
  for (const path of registry.llmsPaths)
    expect(await (await request.get("/llms.txt")).text()).toContain(
      registry.origin + path,
    );
  const redirect = await request.get("/compare?campaign=launch", {
    maxRedirects: 0,
  });
  expect(redirect.status()).toBe(308);
  expect(redirect.headers().location).toBe("/compare/?campaign=launch");
});

test("hydration, mobile navigation, keyboard access and reduced motion work", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await page.getByRole("button", { name: "Decline", exact: true }).click();
  const primary = page.getByRole("navigation", {
    name: "Primary",
    exact: true,
  });
  for (const name of ["Use cases", "MCP gateway", "Compare", "Docs", "Builder"])
    await expect(
      primary.getByRole("link", { name, exact: true }),
    ).toBeVisible();
  const mcp = primary.getByRole("link", { name: "MCP gateway", exact: true });
  await mcp.focus();
  await expect(mcp).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/mcp-gateway\/$/);
  await expect(page).toHaveTitle(
    registry.pages.find((p) => p.path === "/mcp-gateway/")!.title,
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("MCP");
  expect(errors).toEqual([]);
});

test("consent gates the SDK, persists decline, strips sensitive URLs and deduplicates pageviews", async ({
  page,
}) => {
  const { requests, events } = await interceptAnalytics(page);
  await page.goto(
    "/?token=SECRET_URL_MARKER&utm_source=SECRET_URL_MARKER#SECRET_URL_MARKER",
  );
  await expect(
    page.getByRole("complementary", { name: "Optional website analytics" }),
  ).toBeVisible();
  await page.waitForTimeout(5500);
  expect(requests).toHaveLength(0);
  await page.getByRole("button", { name: "Decline", exact: true }).click();
  await page.reload();
  await expect(
    page.getByRole("complementary", { name: "Optional website analytics" }),
  ).toHaveCount(0);
  expect(requests).toHaveLength(0);
  await page
    .getByRole("button", { name: "Analytics preferences", exact: true })
    .click();
  await page.getByRole("button", { name: "Allow", exact: true }).click();
  await expect
    .poll(() => events.filter((e) => e.event === "$pageview").length, {
      timeout: 12000,
    })
    .toBe(1);
  expect(events[0].properties.$current_url).toBe(registry.origin + "/");
  expect(JSON.stringify(events)).not.toContain("SECRET_URL_MARKER");
  await page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("link", { name: "MCP gateway", exact: true })
    .click();
  await expect
    .poll(() => events.filter((e) => e.event === "$pageview").length, {
      timeout: 12000,
    })
    .toBe(2);
  await page
    .getByRole("button", { name: "Analytics preferences", exact: true })
    .click();
  await page.getByRole("button", { name: "Decline", exact: true }).click();
  const stopped = requests.length;
  await page.getByRole("link", { name: "Hookway home", exact: true }).click();
  await page.waitForTimeout(5500);
  expect(requests).toHaveLength(stopped);
  expect(events.filter((e) => e.event === "$pageview")).toHaveLength(2);
});

test("successful copy, builder mount, download and GitHub actions have bounded events", async ({
  page,
  context,
  request,
}) => {
  const { events } = await interceptAnalytics(page);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.goto("/");
  await page.getByRole("button", { name: "Allow", exact: true }).click();
  await expect
    .poll(() => events.filter((e) => e.event === "$pageview").length)
    .toBe(1);
  await page.getByRole("button", { name: "Copy", exact: true }).click();
  await expect
    .poll(() => events.filter((e) => e.event === "quick_start_copied").length)
    .toBe(1);
  await page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("link", { name: "Builder", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Inspector", exact: true }),
  ).toBeVisible();
  await expect
    .poll(() => events.filter((e) => e.event === "builder_opened").length)
    .toBe(1);
  const { BuilderPage } = await import("../e2e/builder");
  const builder = new BuilderPage(page);
  const yaml = await (
    await request.get("/docs/raw/examples/filters.yaml")
  ).text();
  await builder.importYaml(yaml);
  const dialog = await builder.openConfigItem("Export YAML");
  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: /^Download/ }).click();
  const dispatched = await download;
  await expect
    .poll(() => events.filter((e) => e.event === "config_exported").length)
    .toBe(1);
  expect(
    events.find((e) => e.event === "config_exported")!.properties,
  ).toMatchObject({
    method: "download",
    format: dispatched.suggestedFilename().endsWith(".zip") ? "zip" : "yaml",
    page_path: "/builder",
  });
  await page.goto("/");
  await expect
    .poll(() => events.filter((e) => e.event === "$pageview").length)
    .toBe(3);
  await page.route("https://github.com/**", (route) =>
    route.fulfill({ body: "Project link opened" }),
  );
  await page.getByRole("link", { name: "GitHub", exact: true }).click();
  await expect
    .poll(() => events.filter((e) => e.event === "github_opened").length)
    .toBe(1);
  for (const event of events)
    for (const key of [
      "config",
      "payload",
      "spec",
      "filename",
      "$initial_current_url",
      "$session_entry_url",
      "$referrer",
    ])
      expect(event.properties).not.toHaveProperty(key);
});

test('opening the builder before permission is not backfilled after Allow', async ({ page }) => {
  const { events } = await interceptAnalytics(page);
  await page.goto('/builder');
  await expect(page.getByRole('button', { name: 'Inspector', exact: true })).toBeVisible();
  expect(events).toHaveLength(0);
  await page.getByRole('button', { name: 'Allow', exact: true }).click();
  await expect.poll(() => events.filter(e => e.event === '$pageview').length).toBe(1);
  expect(events.filter(e => e.event === 'builder_opened')).toHaveLength(0);
});

test('failed ingest does not create retries after consent is withdrawn', async ({ page }) => {
  const { events, requests } = await interceptAnalytics(page, 503);
  await page.goto('/');
  await page.getByRole('button', { name: 'Allow', exact: true }).click();
  await expect.poll(() => events.filter(e => e.event === '$pageview').length).toBe(1);
  await page.getByRole('button', { name: 'Analytics preferences', exact: true }).click();
  await page.getByRole('button', { name: 'Decline', exact: true }).click();
  const stopped = requests.length;
  await page.waitForTimeout(7000);
  expect(requests).toHaveLength(stopped);
});


test.describe('without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('priority content and navigation remain readable', async ({ page }) => {
    for (const path of [...registry.pages.map(p => p.path), ...registry.requiredDocs]) {
      await page.goto(path);
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await expect(page.getByRole('navigation', { name: 'Primary', exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Builder', exact: true })).toBeVisible();
    }
  });
});
