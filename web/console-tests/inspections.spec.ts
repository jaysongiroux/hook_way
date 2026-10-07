import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { mkdirSync } from "node:fs";
import { resolve } from "node:path";

const API = "http://127.0.0.1:5184";
const OPS = "console-ops-token-0123456789abcdefghijkl";
const READ = "console-read-token-0123456789abcdefghijkl";
const id = (n: number) => `01926d3c-8d6f-7b3a-9c1e-${String(n).padStart(12, "0")}`;
async function login(page: Page, token = OPS) {
  await page.goto("/");
  await page.getByLabel("Replay API token").fill(token);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Failure inbox" })).toBeVisible();
}
async function inspections(page: Page) {
  await page.getByRole("tab", { name: "Inspections", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Inspection inbox" })).toBeVisible();
  await expect(page.getByRole("button", { name: `Inspect ${id(201)}`, exact: true })).toBeVisible();
}
async function select(page: Page, n: number) {
  await page.getByRole("button", { name: `Inspect ${id(n)}`, exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected inspection" })).toContainText("HTTP 503");
}
test.beforeEach(async ({ request }) => { expect((await request.post(`${API}/test/reset`)).status()).toBe(204); });

test("tabs isolate records and preserve independent filters and selection", async ({ page }) => {
  const queries: URL[] = [];
  page.on("request", (r) => { const url = new URL(r.url()); if (url.pathname === "/v1/records") queries.push(url); });
  await login(page);
  await page.getByLabel("Flow", { exact: true }).fill("orders");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await page.getByRole("button", { name: `Inspect ${id(1)}`, exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected failure" })).toBeVisible();
  await inspections(page);
  await expect(page.getByRole("button", { name: `Inspect ${id(1)}`, exact: true })).toHaveCount(0);
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.getByLabel("Flow", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "Filters", exact: true }).click();
  await expect(page.getByLabel("Observed since (inclusive)")).toBeVisible();
  await expect(page.getByLabel("Destination ID", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Captured retry capability")).toHaveCount(0);
  await select(page, 201);
  await page.getByRole("tab", { name: "Failures", exact: true }).click();
  await expect(page.getByLabel("Flow", { exact: true })).toHaveValue("orders");
  await expect(page.getByRole("region", { name: "Selected failure" })).toBeVisible();
  await page.getByRole("tab", { name: "Inspections", exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected inspection" })).toBeVisible();
  expect(queries.some((q) => q.searchParams.get("kind") === "inspection")).toBe(true);
  expect(queries.filter((q) => q.searchParams.get("kind") !== "inspection").every((q) => q.searchParams.get("kind") === "failure")).toBe(true);
});

test("record tabs support keyboard selection and identify their active panel", async ({ page }) => {
  await login(page);
  const failures = page.getByRole("tab", { name: "Failures", exact: true });
  const inspections = page.getByRole("tab", { name: "Inspections", exact: true });
  await failures.focus();
  await page.keyboard.press("ArrowRight");
  await expect(inspections).toBeFocused();
  await expect(inspections).toHaveAttribute("aria-selected", "true");
  await expect(page.getByRole("heading", { name: "Inspection inbox" })).toBeVisible();
  const panelId = await inspections.getAttribute("aria-controls");
  const tabId = await inspections.getAttribute("id");
  await expect(page.locator(`[id="${panelId}"]`)).toHaveAttribute("aria-labelledby", tabId!);
  await page.keyboard.press("ArrowLeft");
  await expect(failures).toBeFocused();
  await expect(page.getByRole("heading", { name: "Failure inbox" })).toBeVisible();
});

test("body and context are explicitly loaded, escaped, exact and downloadable", async ({ page }, info) => {
  let payloadRequests = 0;
  page.on("request", (r) => { if (r.url().endsWith("/payload")) payloadRequests++; });
  await login(page); await inspections(page); await select(page, 201);
  const selected = page.getByRole("region", { name: "Selected inspection" });
  await expect(selected).toContainText("View only");
  await expect(selected.getByRole("button", { name: /Retry|Confirm outcome/ })).toHaveCount(0);
  await expect(page.getByRole("tab", { name: "Related", exact: true })).toHaveCount(0);
  await expect(selected).not.toContainText("Captured error");
  expect(payloadRequests).toBe(0);
  await page.getByRole("button", { name: "Load body and context" }).click();
  await expect(page.getByRole("region", { name: "Captured inspection" })).toContainText("9007199254740993");
  await expect(page.getByRole("region", { name: "Captured inspection" })).toContainText("0.1234567890123456789");
  await expect(page.getByRole("region", { name: "Request context" })).toContainText("/webhooks/orders/42");
  await expect(page.getByRole("region", { name: "Headers", exact: true })).toContainText("[REDACTED]");
  await expect(page.getByRole("region", { name: "Variables", exact: true })).toContainText("north");
  expect(await page.evaluate(() => (window as Window & { payloadExecuted?: boolean }).payloadExecuted)).toBeUndefined();
  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download inspection JSON" }).click();
  const file = await downloaded;
  const data = JSON.parse(await readFile((await file.path())!, "utf8"));
  expect(data.body.content).toContain("9007199254740993");
  expect(data.variables.tenant).toBe("north");
  await file.delete();
  await page.getByRole("button", { name: "Close preview" }).click();
  await expect(page.getByRole("region", { name: "Captured inspection" })).toHaveCount(0);
  await page.getByRole("tab", { name: "Audit history", exact: true }).click();
  await expect(selected).not.toContainText("Retry history");
  await expect(selected).toContainText("payload downloaded");
});

test("read-only scope never requests or displays captured context", async ({ page }) => {
  let payloadRequests = 0;
  page.on("request", (r) => { if (r.url().endsWith("/payload")) payloadRequests++; });
  await login(page, READ); await inspections(page); await select(page, 201);
  await expect(page.getByRole("region", { name: "Selected inspection" })).toContainText("payload scope is required");
  await expect(page.getByRole("button", { name: "Load body and context" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Dismiss inspection" })).toHaveCount(0);
  await expect(page.getByRole("region", { name: "Variables", exact: true })).toHaveCount(0);
  expect(payloadRequests).toBe(0);
});

test("dismissal deletes captured context and retains view-only metadata", async ({ page }) => {
  await login(page); await inspections(page); await select(page, 201);
  await page.getByRole("button", { name: "Dismiss inspection", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Dismiss inspection" });
  await expect(dialog).toContainText("stored body and context");
  await dialog.getByRole("checkbox", { name: "I understand the captured body and context will be deleted." }).check();
  await dialog.getByRole("button", { name: "Dismiss and delete artifacts" }).click();
  await expect(dialog).toContainText("Dismissed");
  await dialog.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected inspection" })).toContainText("Dismissed · View only");
  await expect(page.getByRole("button", { name: "Load body and context" })).toHaveCount(0);
});

test("binary, XML, empty, truncated and metadata-only inspections remain understandable", async ({ page }) => {
  await login(page); await inspections(page);
  for (const [n, text] of [[202, "AAEC/w=="], [205, "The captured body is empty."], [206, "<order><id>42</id>"], [207, "Showing the first 64 KiB"]] as const) {
    await select(page, n);
    await page.getByRole("button", { name: "Load body and context" }).click();
    await expect(page.getByRole("region", { name: "Captured inspection" })).toContainText(text);
    await expect(page.getByRole("region", { name: "Variables", exact: true })).toContainText("north");
  }
  await select(page, 204);
  await page.getByRole("button", { name: "Load body and context" }).click();
  await expect(page.getByRole("alert")).toContainText("1 MiB preview limit");
  await expect(page.getByRole("button", { name: "Download inspection JSON" })).toBeEnabled();
  await select(page, 203);
  await expect(page.getByRole("region", { name: "Selected inspection" })).toContainText("exceeded the record-size limit");
  await expect(page.getByRole("button", { name: "Load body and context" })).toHaveCount(0);
});

test("matching record APIs without inspection support keep the failure workflow", async ({ page }) => {
  await page.route(`${API}/v1/me`, async (route) => {
    const response = await route.fetch(); const me = await response.json();
    me.capabilities = me.capabilities.filter((c: string) => c !== "inspection_records");
    await route.fulfill({ response, json: me });
  });
  const kinds: string[] = [];
  page.on("request", (r) => { const url = new URL(r.url()); if (url.pathname === "/v1/records") kinds.push(url.searchParams.get("kind") || ""); });
  await login(page);
  await expect(page.getByRole("tab", { name: "Inspections", exact: true })).toHaveCount(0);
  expect(kinds.every((kind) => kind === "failure")).toBe(true);
});

for (const [label, width, height] of [["desktop", 1366, 768], ["mobile", 390, 844]] as const) {
  test(`inspection evidence is usable on ${label}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await login(page); await inspections(page); await select(page, 201);
    await page.getByRole("button", { name: "Load body and context" }).click();
    await expect(page.getByRole("heading", { name: "Current body", exact: true })).toBeVisible();
    const selected = page.getByRole("region", { name: "Selected inspection" });
    expect((await selected.locator(".evidence-scroll").boundingBox())!.height).toBeGreaterThan(250);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const directory = resolve("../.impeccable/review"); mkdirSync(directory, { recursive: true });
    await page.screenshot({ path: `${directory}/inspect-${label}.png`, fullPage: true, animations: "disabled" });
    if (label === "mobile") {
      await page.getByRole("button", { name: "Back to inbox" }).click();
      await expect(page.getByRole("button", { name: `Inspect ${id(201)}`, exact: true })).toBeFocused();
    }
  });
}

for (const contract of [0, 2]) {
  test(`API contract ${contract} is rejected before record requests`, async ({ page }) => {
    let recordRequests = 0;
    page.on("request", (request) => { if (new URL(request.url()).pathname === "/v1/records") recordRequests++; });
    await page.route(`${API}/v1/me`, async (route) => {
      const response = await route.fetch(); const me = await response.json();
      await route.fulfill({ response, json: { ...me, contract_version: contract } });
    });
    await page.goto("/");
    await page.getByLabel("Replay API token").fill(OPS);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("requires replay API contract version 1");
    await expect(page.getByRole("heading", { name: "Failure inbox" })).toHaveCount(0);
    expect(recordRequests).toBe(0);
  });
}

test("inspection availability filters use dismissal without delivery states", async ({ page }) => {
  const queries: URL[] = [];
  page.on("request", (request) => { const url = new URL(request.url()); if (url.pathname === "/v1/records" && url.searchParams.get("kind") === "inspection") queries.push(url); });
  await login(page); await inspections(page);
  await expect.poll(() => queries.at(-1)?.searchParams.get("dismissed")).toBe("false");
  expect(queries.every((query) => !query.searchParams.has("delivery_status"))).toBe(true);
  await page.getByRole("tab", { name: "Dismissed", exact: true }).click();
  await expect(page.getByRole("button", { name: `Inspect ${id(208)}`, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: `Inspect ${id(201)}`, exact: true })).toHaveCount(0);
  await expect.poll(() => queries.at(-1)?.searchParams.get("dismissed")).toBe("true");
  await page.getByRole("tab", { name: "All inspections", exact: true }).click();
  await expect(page.getByRole("button", { name: `Inspect ${id(201)}`, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: `Inspect ${id(208)}`, exact: true })).toBeVisible();
  await expect.poll(() => queries.at(-1)?.searchParams.has("dismissed")).toBe(false);
  expect(queries.every((query) => !query.searchParams.has("delivery_status"))).toBe(true);
});
