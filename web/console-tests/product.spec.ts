import { test, expect, type Page } from "@playwright/test";
const API = "http://127.0.0.1:5184";
const TOKEN = "console-ops-token-0123456789abcdefghijkl";
const ID = "01926d3c-8d6f-7b3a-9c1e-000000000001";

async function login(page: Page) {
  await page.goto("/");
  await page.getByLabel("Replay API token").fill(TOKEN);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Failure inbox" })).toBeVisible();
  await page.getByLabel("Flow", { exact: true }).fill("orders");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
  await expect(page.getByRole("status")).toHaveCount(0);
}
test.beforeEach(async ({ request }) => { expect((await request.post(`${API}/test/reset`)).status()).toBe(204); });

test("runtime configuration rejects unsafe URLs before sending credentials and can retry", async ({ page }) => {
  const apiRequests: string[] = [];
  page.on("request", (request) => { if (request.url().startsWith(API)) apiRequests.push(request.url()); });
  await page.route("**/runtime-config.json", (route) => route.fulfill({ json: { replayApiUrl: "https://operator:secret@api.example" } }));
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("Product configuration unavailable");
  expect(apiRequests).toEqual([]);
  await expect(page.getByLabel("Replay API token")).toHaveCount(0);
  await page.unroute("**/runtime-config.json");
  await page.getByRole("button", { name: "Retry configuration" }).click();
  await expect(page.getByLabel("Replay API token")).toBeVisible();
});

test("builder opens without authentication and local WASM recovery keeps editing available", async ({ page }) => {
  const calls: string[] = [];
  page.on("request", (request) => { if (request.url().startsWith(API) || /\/(validate|validate-replay|replay-schema)$/.test(new URL(request.url()).pathname)) calls.push(request.url()); });
  await page.route("**/hookway.wasm", (route) => route.fulfill({ status: 503, body: "Unavailable" }));
  await page.goto("/builder");
  await expect(page.locator(".hookway-builder-root")).toBeVisible();
  await expect(page.getByRole("alert", { name: "Local validation unavailable" })).toBeVisible();
  await page.getByRole("button", { name: "Config", exact: true }).click();
  await expect(page.getByRole("menuitem", { name: "Import YAML", exact: true })).toBeEnabled();
  await expect(page.getByRole("menuitem", { name: "Export YAML", exact: true })).toBeEnabled();
  await page.keyboard.press("Escape");
  expect(calls).toEqual([]);
  await page.unroute("**/hookway.wasm");
  await page.getByRole("button", { name: "Retry local validation" }).click();
  await expect(page.getByRole("alert", { name: "Local validation unavailable" })).toHaveCount(0);
  expect(calls).toEqual([]);
});

test("route switches keep investigation state and pause API polling until return", async ({ page }) => {
  let polls = 0;
  page.on("request", (request) => { if (new URL(request.url()).pathname === "/v1/records") polls++; });
  await login(page);
  await page.getByRole("button", { name: `Inspect ${ID}`, exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected failure" })).toBeVisible();
  await page.getByRole("link", { name: "Builder", exact: true }).click();
  await expect(page.locator(".hookway-builder-root")).toBeVisible();
  await expect(page.getByRole("region", { name: "Selected failure" })).toHaveCount(0);
  const paused = polls;
  await page.waitForTimeout(3300);
  expect(polls).toBe(paused);
  await page.getByRole("link", { name: "Replay console", exact: true }).click();
  await expect(page.getByLabel("Flow", { exact: true })).toHaveValue("orders");
  await expect(page.getByRole("region", { name: "Selected failure" })).toBeVisible();
  await expect.poll(() => polls).toBeGreaterThan(paused);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Failure inbox" })).toBeVisible();
  await expect(page.getByLabel("Flow", { exact: true })).toHaveValue("");
  await expect(page.getByRole("region", { name: "Selected failure" })).toHaveCount(0);
});

test("a new builder flow survives leaving before its save debounce expires", async ({ page }) => {
  await page.goto("/builder");
  await expect(page.locator(".hookway-builder-root")).toBeVisible();
  await expect(page.getByRole("button", { name: "Active flow" })).toBeVisible();
  await page.getByRole("button", { name: "Active flow" }).click();
  await page.getByRole("menuitem", { name: "New flow", exact: true }).click();
  await page.getByLabel("New flow name").fill("route_preserved_draft");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  await page.getByRole("link", { name: "Replay console", exact: true }).click();
  await page.getByRole("link", { name: "Builder", exact: true }).click();
  await expect(page.getByRole("button", { name: "Active flow" })).toHaveText("route_preserved_draft");
});

test("an uncertain mutation survives builder navigation without automatic resubmission", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: `Inspect ${ID}`, exact: true }).click();
  await page.getByRole("button", { name: "Retry delivery", exact: true }).click();
  const bodies: (string | null)[] = [];
  await page.route(`**/v1/records/${ID}/retries`, async (route) => {
    bodies.push(route.request().postData());
    if (bodies.length === 1) { await route.fetch(); await route.abort("failed"); }
    else await route.continue();
  });
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("Response uncertain");
  await page.getByRole("button", { name: "Close and inspect history" }).click();
  await page.getByRole("link", { name: "Builder", exact: true }).click();
  await expect(page.locator(".hookway-builder-root")).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("link", { name: "Replay console", exact: true }).click();
  await page.getByRole("button", { name: "Continue uncertain request" }).click();
  await expect(page.getByRole("dialog")).toContainText("Response uncertain");
  expect(bodies).toHaveLength(1);
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(page.getByRole("dialog")).toContainText("Queued retry");
  expect(bodies).toHaveLength(2);
  expect(bodies[0]).toEqual(bodies[1]);
});

test("CORS or network failure gives deployment guidance", async ({ page }) => {
  await page.route(`${API}/v1/me`, (route) => route.abort("failed"));
  await page.goto("/");
  await page.getByLabel("Replay API token").fill(TOKEN);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("API CORS configuration");
});

for (const size of [
  { width: 1366, height: 768, label: "1366-768" },
  { width: 1280, height: 720, label: "1280-720" },
  { width: Math.floor(1366 / 1.25), height: Math.floor(768 / 1.25), label: "zoom-125" },
  { width: Math.floor(1366 / 1.5), height: Math.floor(768 / 1.5), label: "zoom-150" },
  { width: 390, height: 844, label: "mobile" },
]) {
  test(`evidence and builder use available viewport at ${size.label}`, async ({ page }, info) => {
    // Browser zoom reduces the CSS viewport by the zoom factor; these viewports
    // exercise that same responsive geometry without changing OS display scale.
    await page.setViewportSize(size);
    await login(page);
    if (size.width > 1000) {
      const panel = page.getByRole("region", { name: "Investigation panel" });
      const before = await panel.boundingBox();
      await page.getByRole("button", { name: "Filters", exact: true }).click();
      await expect(page.getByLabel("Node ID")).toBeVisible();
      expect(await panel.boundingBox()).toEqual(before);
    }
    await page.getByRole("button", { name: `Inspect ${ID}`, exact: true }).click();
    const selected = page.getByRole("region", { name: "Selected failure" });
    await expect(selected.getByRole("button", { name: "Retry delivery", exact: true })).toBeVisible();
    const panel = await selected.boundingBox();
    // The new record-kind navigation occupies one compact row above evidence.
    // Account for that row while retaining the existing viewport-space budget.
    const recordNavigation = await page.getByRole("tablist", { name: "Record kind" }).boundingBox();
    expect(recordNavigation!.height).toBeLessThan(54);
    expect(panel!.y).toBeLessThan(105 + recordNavigation!.height);
    expect(panel!.height).toBeGreaterThan(size.height - 125 - recordNavigation!.height);
    const evidence = await selected.locator(".evidence-scroll").first().boundingBox();
    expect(evidence!.height).toBeGreaterThan(size.height < 600 ? 130 : 250);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: info.outputPath(`product-${size.label}.png`), fullPage: true });
    await page.getByRole("link", { name: "Builder", exact: true }).click();
    await expect(page.locator(".hookway-builder-root")).toBeVisible();
    await expect(page.locator("header")).toHaveCount(1);
    const header = page.locator("header");
    await expect(header.getByRole("button", { name: "Config", exact: true })).toBeVisible();
    await expect(header.getByRole("link", { name: "Replay console", exact: true })).toBeVisible();
    await expect(header.getByRole("button", { name: "demo-operator", exact: true })).toBeVisible();
    const nodes = header.getByRole("button", { name: "Nodes", exact: true });
    if (await nodes.getAttribute("aria-pressed") === "true") await nodes.click();
    const canvas = await page.locator(".react-flow").boundingBox();
    const headerBounds = await header.boundingBox();
    expect(canvas!.y).toBeCloseTo(headerBounds!.y + headerBounds!.height, 0);
    expect(canvas!.y + canvas!.height).toBeCloseTo(size.height, 0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: info.outputPath(`builder-${size.label}.png`), fullPage: true });
    await header.getByRole("button", { name: "demo-operator", exact: true }).click();
    await expect(page.getByRole("menuitem", { name: "Sign out", exact: true })).toBeVisible();
  });
}
