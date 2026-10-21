import { test, expect } from "@playwright/test";
const OPS = "console-ops-token-0123456789abcdefghijkl";
const READ = "console-read-token-0123456789abcdefghijkl";
async function signIn(page: import("@playwright/test").Page, token = OPS) {
  await page.goto("/"); await page.getByLabel("Replay API token").fill(token); await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("tab", { name: "MCP events", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Event subscriptions" })).toBeVisible();
}
test.beforeEach(async ({ request }) => { expect((await request.post("http://127.0.0.1:5184/test/reset")).status()).toBe(204); });
for (const width of [1440, 390]) {
  test(`inspect, revoke, restore and navigate event evidence at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 }); await signIn(page);
    await page.getByRole("button", { name: /issue.opened.*sub_console/ }).click();
    const evidence = page.getByRole("region", { name: "Event evidence" });
    await expect(evidence.getByText("https://receiver.example.test", { exact: true })).toBeVisible();
    await expect(page.getByText("secret-path")).toHaveCount(0);
    await page.screenshot({ path: info.outputPath(`events-subscription-${width}.png`), fullPage: true });
    await evidence.getByRole("button", { name: "Revoke subscription" }).click();
    await page.getByRole("textbox", { name: /Reason/ }).fill("Pause this subscription"); await page.getByRole("button", { name: "Confirm", exact: true }).click();
    await expect(evidence.getByRole("button", { name: "Restore subscription" })).toBeEnabled();
    await evidence.getByRole("button", { name: "Restore subscription" }).click();
    await expect(page.getByRole("dialog")).toContainText("will not reactivate monitoring");
    await page.getByRole("textbox", { name: /Reason/ }).fill("Permit a new subscription"); await page.getByRole("button", { name: "Confirm", exact: true }).click();
    await expect(evidence.getByRole("button", { name: "Restore subscription" })).toBeDisabled();
    await evidence.getByRole("button", { name: "View related deliveries" }).click();
    if (width < 1000 && await page.getByRole("button", { name: "Back to inbox" }).isVisible()) await page.getByRole("button", { name: "Back to inbox" }).click();
    await page.getByRole("button", { name: /issue.opened.*failed/ }).click();
    await expect(evidence.getByText("console-issue", { exact: true })).toBeVisible();
    await expect(evidence.getByRole("button", { name: "Retry delivery" })).toBeDisabled();
    await expect(evidence.getByText("Earlier attempts were not recorded", { exact: false })).toBeVisible();
    await page.screenshot({ path: info.outputPath(`events-delivery-${width}.png`), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
}
test("failed delivery retry requires acknowledgement and payload loading is explicit", async ({ page }) => {
  const payloads: string[] = []; page.on("request", (req) => { if (req.url().includes("mcp-events") && req.url().endsWith("/payload")) payloads.push(req.url()); });
  await signIn(page); await page.getByRole("tab", { name: "Deliveries", exact: true }).click();
  await page.getByRole("button", { name: /issue.opened.*failed/ }).click();
  const evidence = page.getByRole("region", { name: "Event evidence" }); expect(payloads).toHaveLength(0);
  await evidence.getByRole("button", { name: "Load event payload" }).click(); await expect(evidence.locator("pre")).toContainText("console-issue");
  await evidence.getByRole("button", { name: "Retry delivery" }).click(); await expect(page.getByRole("button", { name: "Confirm", exact: true })).toBeDisabled();
  await page.getByRole("checkbox", { name: /acknowledge/ }).check(); await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(evidence.getByText("1 lifetime", { exact: false })).toHaveCount(0);
  await expect(evidence.getByText("2 lifetime · 0 in cycle 2", { exact: true })).toBeVisible();
});
for (const width of [1440, 390]) {
  test(`reader permission explanations at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page, READ); await page.getByRole("button", { name: /issue.opened.*sub_console/ }).click();
    await expect(page.getByRole("button", { name: "Revoke subscription" })).toBeDisabled();
    await expect(page.getByText("Revoke and restore require events_manage permission.")).toBeVisible();
    await page.screenshot({ path: info.outputPath(`events-reader-subscription-${width}.png`), fullPage: true });
    await page.getByRole("button", { name: "View related deliveries" }).click();
    const back = page.getByRole("button", { name: "Back to inbox" });
    if (width < 1000 && await back.isVisible()) await back.click();
    await page.getByRole("button", { name: /issue.opened.*failed/ }).click();
    await expect(page.getByRole("button", { name: "Retry delivery" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Load event payload" })).toBeDisabled();
    await expect(page.getByText("Payload access requires events_payload permission.")).toBeVisible();
    await page.screenshot({ path: info.outputPath(`events-reader-delivery-${width}.png`), fullPage: true });
  });
}
test("unsupported event core leaves normal replay available", async ({ page }) => {
  await page.route("**/v1/mcp-events/status", (route) => route.fulfill({ json: { state: "unsupported" } })); await signIn(page);
  await expect(page.getByRole("alert")).toContainText("Core does not support event operations");
  await page.getByRole("tab", { name: "Failures", exact: true }).click(); await expect(page.getByRole("heading", { name: "Failure inbox" })).toBeVisible();
});

test("lost mutation response reuses its receipt without a second revocation", async ({ page }) => {
  const keys: string[] = [];
  await page.route("**/v1/mcp-events/subscriptions/*/revoke", async (route) => {
    keys.push(route.request().headers()["idempotency-key"]);
    const response = await route.fetch();
    if (keys.length === 1) await route.abort("failed");
    else await route.fulfill({ response });
  });
  await signIn(page);
  await page.getByRole("button", { name: /issue.opened.*sub_console/ }).click();
  await page.getByRole("button", { name: "Revoke subscription" }).click();
  await page.getByRole("textbox", { name: /Reason/ }).fill("Recover ambiguous response");
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("Outcome not confirmed");
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Restore subscription" })).toBeEnabled();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  await expect(page.getByRole("region", { name: "Event evidence" }).getByText(/revoke · demo-operator/)).toHaveCount(1);
});
test("leaving delivery evidence cancels a late payload response", async ({ page }) => {
  let release!: () => void;
  let captured!: () => void;
  let completed!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  const requested = new Promise<void>((resolve) => { captured = resolve; });
  const finished = new Promise<void>((resolve) => { completed = resolve; });
  await page.route("**/v1/mcp-events/deliveries/*/payload", async (route) => {
    const response = await route.fetch(); captured(); await gate;
    try { await route.fulfill({ response }); } catch { /* Selection cancelled this read. */ }
    finally { completed(); }
  });
  await signIn(page); await page.getByRole("tab", { name: "Deliveries", exact: true }).click();
  await page.getByRole("button", { name: /issue.opened.*failed/ }).click();
  const evidence = page.getByRole("region", { name: "Event evidence" });
  await evidence.getByRole("button", { name: "Load event payload" }).click(); await requested;
  await evidence.getByRole("button", { name: "sub_console", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Event subscriptions" })).toBeVisible();
  release(); await finished;
  await page.getByRole("tab", { name: "Deliveries", exact: true }).click();
  await expect(evidence.getByText("console-issue", { exact: true })).toBeVisible();
  await expect(evidence.locator("pre")).toHaveCount(0);
});

test("linked investigation preserves applied delivery filters and selection", async ({ page }) => {
  await signIn(page); await page.getByRole("tab", { name: "Deliveries", exact: true }).click();
  await page.getByRole("textbox", { name: "Event", exact: true }).fill("issue.opened");
  await page.getByRole("button", { name: "Apply filters", exact: true }).click();
  await page.getByRole("button", { name: /issue.opened.*failed/ }).click();
  const evidence = page.getByRole("region", { name: "Event evidence" });
  await evidence.getByRole("button", { name: "sub_console", exact: true }).click();
  await evidence.getByRole("button", { name: "View related deliveries" }).click();
  await expect(page.getByRole("textbox", { name: "Event", exact: true })).toHaveValue("issue.opened");
  await expect(evidence.getByText("console-issue", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /issue.opened.*failed/ })).toHaveAttribute("aria-pressed", "true");
});
