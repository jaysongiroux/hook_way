import {
  test,
  expect,
  type Page,
  type APIRequestContext,
} from "@playwright/test";
import fs from "node:fs/promises";
const API_URL = "http://127.0.0.1:5184";
const OPS = "console-ops-token-0123456789abcdefghijkl";
const READ = "console-read-token-0123456789abcdefghijkl";
const RETRY = "console-retry-token-0123456789abcdefghijkl";
const id = (n: number) =>
  `01926d3c-8d6f-7b3a-9c1e-${String(n).padStart(12, "0")}`;
const headers = { Authorization: `Bearer ${OPS}` };
async function signIn(page: Page, token = OPS) {
  await page.goto("/");
  await page.getByLabel("Replay API token").fill(token);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: `Inspect ${id(1)}`, exact: true }),
  ).toBeVisible();
  await page.getByLabel("Flow", { exact: true }).fill("orders");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
}
async function status(page: Page, label: string) {
  await page.getByRole("tab", { name: label, exact: true }).click();
}
async function detail(request: APIRequestContext, n: number) {
  const response = await request.get(`${API_URL}/v1/records/${id(n)}`, { headers });
  expect(response.ok()).toBe(true);
  return response.json();
}
test.beforeEach(async ({ request }) => {
  const response = await request.post(`${API_URL}/test/reset`);
  expect(response.status()).toBe(204);
});

test("inspect a failure and explicitly preview/download exact, escaped payload bytes", async ({
  page,
  request,
}, info) => {
  const payloadRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/payload")) payloadRequests.push(request.url());
  });
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(
    selected.getByText("Retry scope: captured destination only"),
  ).toBeVisible();
  expect(payloadRequests).toHaveLength(0);
  await page.screenshot({
    path: info.outputPath("console-failure-desktop.png"),
    fullPage: true,
  });
  await selected.getByRole("button", { name: "Preview payload" }).click();
  const modal = page.getByRole("dialog");
  await expect(modal.locator("pre")).toContainText("9007199254740993");
  await expect(modal.locator("pre")).toContainText("0.1234567890123456789");
  await expect(modal.locator("pre")).toContainText(
    "<script>window.payloadExecuted=true</script>",
  );
  expect(await page.evaluate(() => "payloadExecuted" in window)).toBe(false);
  await page.keyboard.press("Escape");
  const downloadEvent = page.waitForEvent("download");
  await selected
    .getByRole("button", { name: "Download original bytes" })
    .click();
  const download = await downloadEvent;
  const bytes = await fs.readFile((await download.path())!);
  expect(bytes.toString()).toContain("9007199254740993");
  const record = await detail(request, 1);
  expect(
    record.history.filter(
      (event: { event: string }) => event.event === "payload_downloaded",
    ),
  ).toHaveLength(2);
});

test("lost retry response keeps its key and body across inspection and resubmission", async ({
  page,
  request,
}, info) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Retry delivery", exact: true })
    .click();
  const sent: { key: string | undefined; body: string | null }[] = [];
  let lose = true;
  await page.route(`**/v1/records/${id(1)}/retries`, async (route) => {
    sent.push({
      key: route.request().headers()["idempotency-key"],
      body: route.request().postData(),
    });
    if (lose) {
      lose = false;
      const response = await route.fetch();
      expect(response.status()).toBe(202);
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  await expect(
    page.getByRole("alert", {
      name: "Response uncertain — the action may have been accepted",
      exact: true,
    }),
  ).toContainText("Response uncertain");
  await page.getByRole("button", { name: "Close and inspect history" }).click();
  await expect(
    page.getByRole("button", { name: "Continue uncertain request" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Continue uncertain request" })
    .click();
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(
    page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
  ).toContainText("Queued retry");
  expect(sent).toHaveLength(2);
  expect(sent[0]).toEqual(sent[1]);
  const record = await detail(request, 1);
  expect(record.runs).toHaveLength(1);
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toContainText("Retry in progress");
  await page.screenshot({
    path: info.outputPath("console-retry-accepted.png"),
    fullPage: true,
  });
});

test("batch displays accepted and stale-state outcomes for exactly the selected IDs", async ({
  page,
  request,
}) => {
  await signIn(page);
  await page.getByLabel(`Select ${id(1)}`, { exact: true }).check();
  await page.getByLabel(`Select ${id(2)}`, { exact: true }).check();
  await page.getByRole("button", { name: "Retry selected (2)" }).click();
  const stale = await request.post(`${API_URL}/v1/records/${id(2)}/retries`, {
    headers: { ...headers, "Idempotency-Key": "another-operator" },
    data: {},
  });
  expect(stale.status()).toBe(202);
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  const results = page.getByRole("alert");
  await expect(results).toContainText(`${id(1)}: queued`);
  await expect(results).toContainText(`${id(2)}: run_active`);
  expect((await detail(request, 1)).runs).toHaveLength(1);
  expect((await detail(request, 2)).runs).toHaveLength(2);
});

test("unknown retry requires explicit duplicate-risk acknowledgement", async ({
  page,
  request,
}, info) => {
  await signIn(page);
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toContainText("Delivery may have happened");
  await page
    .getByRole("button", { name: "Retry delivery", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Queue retry", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: info.outputPath("console-unknown-confirmation.png"),
    fullPage: true,
  });
  await page
    .getByLabel(
      "I understand the destination may already have received this delivery and another retry can create a duplicate.",
    )
    .check();
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  await expect(
    page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
  ).toContainText("Queued retry");
  const record = await detail(request, 3);
  expect(record.runs[0].acknowledge_duplicate_risk).toBe(true);
});

test("operator confirmation preserves unknown history and evidence", async ({
  page,
  request,
}, info) => {
  await signIn(page);
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toContainText("Delivery may have happened");
  await page.screenshot({
    path: info.outputPath("console-unknown-desktop.png"),
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Confirm outcome", exact: true })
    .click();
  await page
    .getByLabel("Evidence note")
    .fill("Destination audit confirms receipt for evt-demo-3.");
  await page.getByLabel("Receipt or incident reference").fill("INC-DEMO-42");
  await page
    .getByRole("button", { name: "Record confirmation", exact: true })
    .click();
  await expect(
    page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
  ).toContainText("Resolved — operator confirmed");
  await page.getByRole("button", { name: "Done", exact: true }).click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(selected).toContainText("INC-DEMO-42");
  const record = await detail(request, 3);
  expect(record.delivery_status).toBe("resolved");
  expect(record.runs[0].status).toBe("unknown");
  expect(record.artifacts.stored).toBe(true);
  expect(record.resolution.actor).toBe("demo-operator");
  await page.screenshot({
    path: info.outputPath("console-operator-confirmed.png"),
    fullPage: true,
  });
});

test("confirmation rejects eligibility that changed after opening the dialog", async ({
  page,
  request,
}) => {
  await signIn(page);
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Confirm outcome", exact: true })
    .click();
  await page.getByLabel("Evidence note").fill("Receipt confirmed externally.");
  const retry = await request.post(`${API_URL}/v1/records/${id(3)}/retries`, {
    headers: { ...headers, "Idempotency-Key": "concurrent-operator" },
    data: { acknowledge_duplicate_risk: true },
  });
  expect(retry.status()).toBe(202);
  await page
    .getByRole("button", { name: "Record confirmation", exact: true })
    .click();
  await expect(
    page
      .getByRole("dialog")
      .getByRole("alert", { name: "Action was not confirmed" }),
  ).toContainText("Action was not confirmed");
  expect((await detail(request, 3)).resolution).toBeUndefined();
});

test("dismissal confirms artifact deletion while retaining audit history", async ({
  page,
  request,
}, info) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Dismiss failure", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Dismiss and delete artifacts" }),
  ).toBeDisabled();
  await page
    .getByLabel("I understand the stored payload and snapshot will be deleted.")
    .check();
  await page
    .getByRole("button", { name: "Dismiss and delete artifacts" })
    .click();
  await expect(
    page.getByRole("dialog").getByRole("alert", { name: "Action results" }),
  ).toContainText("Dismissed.");
  await page.getByRole("button", { name: "Done", exact: true }).click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(selected).toContainText("Dismissed");
  expect((await detail(request, 1)).artifacts.stored).toBe(false);
  await page.screenshot({
    path: info.outputPath("console-dismissed.png"),
    fullPage: true,
  });
});

test("read-only and recovery-only scopes restrict actions and payload access", async ({
  page,
}) => {
  await signIn(page, READ);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toContainText("Read-only access");
  await expect(
    page.getByRole("button", { name: "Retry delivery", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Preview payload", exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: /^demo-(operator|reader|recovery)$/ })
    .click();
  await page.getByRole("menuitem", { name: "Sign out", exact: true }).click();
  await signIn(page, RETRY);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Retry delivery", exact: true }),
  ).toBeEnabled();
  await expect(
    page.getByRole("button", { name: "Preview payload", exact: true }),
  ).toHaveCount(0);
});

test("WASM outage supports explicit API input validation", async ({ page }) => {
  await page.route("**/hookway.wasm", (route) =>
    route.fulfill({ status: 503, body: "Unavailable" }),
  );
  await signIn(page);
  await expect(page.getByRole("alert")).toContainText(
    "Local validation is unavailable",
  );
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Retry delivery", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("button", { name: "Queue retry" }),
  ).toBeDisabled();
  await dialog.getByLabel("Use API input validation for this request").check();
  const validation = page.waitForResponse(
    (response) =>
      response.url().endsWith("/v1/actions/validate") &&
      response.status() === 200,
  );
  await dialog.getByRole("button", { name: "Queue retry" }).click();
  await validation;
  await expect(dialog.getByRole("alert").last()).toContainText("Queued retry");
});

test("reload and sign-out clear credentials and all operational state", async ({
  page,
}) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(1)}`, exact: true })
    .click();
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toBeVisible();
  expect(
    await page.evaluate(() => ({
      local: localStorage.length,
      session: sessionStorage.length,
      cookie: document.cookie,
    })),
  ).toEqual({ local: 0, session: 0, cookie: "" });
  await page.reload();
  await expect(page.getByLabel("Replay API token")).toHaveValue("");
  await expect(
    page.getByRole("region", { name: "Selected failure" }),
  ).toHaveCount(0);
  await signIn(page);
  await page
    .getByRole("button", { name: /^demo-(operator|reader|recovery)$/ })
    .click();
  await page.getByRole("menuitem", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toHaveCount(0);
});

test("cursor pagination filters exact flow and keeps a bounded page", async ({
  page,
}) => {
  await signIn(page);
  await page.getByLabel("Flow", { exact: true }).fill("archive");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(50);
  await page.getByRole("button", { name: "Next page" }).click();
  await expect(page.getByText("Page 2", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(11);
  await page.getByRole("button", { name: "Previous page" }).click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(50);
});

test("mobile layout and keyboard recovery controls stay usable", async ({
  page,
}, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await status(page, "Unknown outcome");
  const inspect = page.getByRole("button", {
    name: `Inspect ${id(3)}`,
    exact: true,
  });
  await inspect.focus();
  await page.keyboard.press("Enter");
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(selected).toBeVisible();
  await selected.getByRole("button", { name: "Confirm outcome" }).click();
  await expect(page.getByLabel("Evidence note")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: info.outputPath("console-unknown-mobile.png"),
    fullPage: true,
  });
});

test("payload previews are bounded and binary content requires download", async ({
  page,
}) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(7)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Preview payload", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText("first 64 KiB");
  expect(
    (await page.getByRole("dialog").locator("pre").textContent())?.length,
  ).toBe(65536);
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: `Inspect ${id(8)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Preview payload", exact: true })
    .click();
  await expect(
    page.getByRole("alert", { name: "Payload unavailable", exact: true }),
  ).toContainText("Binary payload");
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("replay API serves no console assets", async ({ request }) => {
  expect((await request.get(`${API_URL}/console/`)).status()).toBe(404);
});

test("lost batch and resolution responses reuse identical bodies and keys", async ({
  page,
  request,
}) => {
  await signIn(page);
  await page.getByLabel(`Select ${id(1)}`, { exact: true }).check();
  await page.getByLabel(`Select ${id(2)}`, { exact: true }).check();
  await page.getByRole("button", { name: "Retry selected (2)" }).click();
  const batches: { key: string | undefined; body: string | null }[] = [];
  let firstBatch = true;
  await page.route("**/v1/retries", async (route) => {
    batches.push({
      key: route.request().headers()["idempotency-key"],
      body: route.request().postData(),
    });
    if (firstBatch) {
      firstBatch = false;
      expect((await route.fetch()).status()).toBe(200);
      await route.abort("failed");
    } else await route.continue();
  });
  await page.getByRole("button", { name: "Queue retry", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("Response uncertain");
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(page.getByRole("dialog")).toContainText(`${id(1)}: queued`);
  expect(batches).toHaveLength(2);
  expect(batches[0]).toEqual(batches[1]);
  expect((await detail(request, 1)).runs).toHaveLength(1);
  expect((await detail(request, 2)).runs).toHaveLength(2);
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  await page
    .getByRole("button", { name: "Confirm outcome", exact: true })
    .click();
  await page
    .getByLabel("Evidence note")
    .fill("Verified receipt with the destination audit team.");
  const resolutions: { key: string | undefined; body: string | null }[] = [];
  let firstResolution = true;
  await page.route(`**/v1/records/${id(3)}/resolve`, async (route) => {
    resolutions.push({
      key: route.request().headers()["idempotency-key"],
      body: route.request().postData(),
    });
    if (firstResolution) {
      firstResolution = false;
      expect((await route.fetch()).status()).toBe(200);
      await route.abort("failed");
    } else await route.continue();
  });
  await page
    .getByRole("button", { name: "Record confirmation", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText("Response uncertain");
  await expect(page.getByLabel("Evidence note")).toBeDisabled();
  await page.getByRole("button", { name: "Resubmit same request" }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "Resolved — operator confirmed",
  );
  expect(resolutions).toHaveLength(2);
  expect(resolutions[0]).toEqual(resolutions[1]);
  const record = await detail(request, 3);
  expect(
    record.history.filter(
      (event: { event: string }) => event.event === "operator_resolved",
    ),
  ).toHaveLength(1);
});

test("visible-page polling stops while hidden and resumes on visibility", async ({
  page,
}) => {
  let polls = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/v1/records") polls++;
  });
  await signIn(page);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "hidden",
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  const hiddenPolls = polls;
  // A full poll interval is necessary to prove no timer initiated hidden work.
  await page.waitForTimeout(3300);
  expect(polls).toBe(hiddenPolls);
  await page.evaluate(() => {
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => polls).toBeGreaterThan(hiddenPolls);
  await expect
    .poll(() => polls, { timeout: 6000 })
    .toBeGreaterThan(hiddenPolls + 1);
});

test("blocked runs, retry success and child relationships explain recovery", async ({
  page,
}, info) => {
  await signIn(page);
  await page
    .getByRole("button", { name: `Inspect ${id(2)}`, exact: true })
    .click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(selected).toContainText(
    "The retry was blocked before delivery.",
  );
  await page.screenshot({
    path: info.outputPath("console-blocked.png"),
    fullPage: true,
  });
  await status(page, "Resolved");
  await page
    .getByRole("button", { name: `Inspect ${id(4)}`, exact: true })
    .click();
  await expect(selected).toContainText("Resolved — retry succeeded");
  await selected.getByRole("tab", { name: "Related (1)", exact: true }).click();
  await selected
    .getByRole("button", { name: `Inspect child ${id(100)}`, exact: true })
    .click();
  await expect(selected.getByText(id(100), { exact: true })).toBeVisible();
  await selected.getByRole("tab", { name: "Related", exact: true }).click();
  await selected
    .getByRole("button", { name: "Inspect parent", exact: true })
    .click();
  await expect(selected.getByText(id(4), { exact: true })).toBeVisible();
  await page.screenshot({
    path: info.outputPath("console-retry-succeeded.png"),
    fullPage: true,
  });
});

test("semantic text has readable contrast and resolved labels remain complete", async ({
  page,
}) => {
  await signIn(page);
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await expect(
    selected.getByRole("button", { name: "Dismiss failure", exact: true }),
  ).toBeVisible();
  const ratios = await selected.evaluate((element) => {
    const color = (css: string) => {
      const values = css.match(/[\d.]+/g)!.map(Number);
      const alpha = values.length === 4 ? values[3] : 1;
      return values
        .slice(0, 3)
        .map((value) => alpha * value + (1 - alpha) * 255);
    };
    const luminance = (rgb: number[]) =>
      rgb
        .map((value) => {
          const n = value / 255;
          return n <= 0.04045 ? n / 12.92 : ((n + 0.055) / 1.055) ** 2.4;
        })
        .reduce(
          (sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index],
          0,
        );
    const ratio = (foreground: string, background: string) => {
      const a = luminance(color(foreground)),
        b = luminance(color(background));
      return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    };
    const badge = element.querySelector(".delivery-state")!;
    const title = element.querySelector(".mantine-Alert-title")!;
    const alert = title.closest(".mantine-Alert-root")!;
    const dismiss = [...element.querySelectorAll("button")].find(
      (button) => button.textContent === "Dismiss failure",
    )!;
    return [
      ratio(
        getComputedStyle(badge).color,
        getComputedStyle(badge).backgroundColor,
      ),
      ratio(
        getComputedStyle(title).color,
        getComputedStyle(alert).backgroundColor,
      ),
      ratio(getComputedStyle(dismiss).color, "rgb(255,255,255)"),
    ];
  });
  for (const ratio of ratios) expect(ratio).toBeGreaterThanOrEqual(4.5);
  await status(page, "Resolved");
  const inbox = page.getByRole("region", { name: "Failure inbox" });
  await expect(inbox.locator(".delivery-state")).toHaveText(
    "Resolved — retry succeeded",
  );
  expect(
    await inbox
      .locator(".mantine-Badge-label")
      .evaluate((element) => getComputedStyle(element).textOverflow),
  ).toBe("clip");
});

for (const width of [1440, 1272, 390]) {
  test(`investigation workspace preserves context and exposes recovery at ${width}px`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    await expect(page.getByLabel("Node ID", { exact: true })).not.toBeVisible();
    if (width > 1000)
      await expect(
        page.getByRole("heading", { name: "Select a delivery to investigate" }),
      ).toBeVisible();
    await page.screenshot({
      path: info.outputPath(`inbox-${width}.png`),
      fullPage: true,
    });
    const trigger = page.getByRole("button", {
      name: `Inspect ${id(1)}`,
      exact: true,
    });
    await trigger.click();
    const selected = page.getByRole("region", { name: "Selected failure" });
    await expect(
      selected.getByRole("button", { name: "Retry delivery", exact: true }),
    ).toBeVisible();
    const measurements = await selected
      .getByRole("button", { name: "Retry delivery", exact: true })
      .evaluate((button) => ({
        bottom: button.getBoundingClientRect().bottom,
        height: innerHeight,
      }));
    expect(measurements.bottom).toBeLessThan(measurements.height);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollHeight <= innerHeight + 1,
      ),
    ).toBe(true);
    if (width > 1000) await expect(trigger).toBeVisible();
    else
      await expect(
        page.getByRole("region", { name: "Failure inbox" }),
      ).not.toBeVisible();
    await page.screenshot({
      path: info.outputPath(`detail-${width}.png`),
      fullPage: true,
    });
    await selected
      .getByRole("button", {
        name: width > 1000 ? "Close details" : "Back to inbox",
        exact: true,
      })
      .click();
    await expect(trigger).toBeFocused();
    await expect(page.getByLabel("Flow", { exact: true })).toHaveValue(
      "orders",
    );
    await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(
      5,
    );
  });
}

test("secondary filters are deliberate and invalid bounds stay distinct from refresh errors", async ({
  page,
}) => {
  await signIn(page);
  await page.getByRole("button", { name: "Filters", exact: true }).click();
  await expect(
    page.getByLabel("Destination ID", { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("Failed since (inclusive)", { exact: true })
    .fill("2026-10-04T12:00");
  await page
    .getByLabel("Failed before (exclusive)", { exact: true })
    .fill("2026-10-04T11:00");
  await expect(
    page.getByText("Unapplied changes", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(
    page.getByRole("alert", { name: "Check your filters", exact: true }),
  ).toContainText("lower time bound");
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
  await page.getByLabel("Failed since (inclusive)", { exact: true }).fill("");
  await page.getByLabel("Failed before (exclusive)", { exact: true }).fill("");
  await page
    .getByLabel("Destination ID", { exact: true })
    .fill("missing-destination");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(
    page.getByRole("region", { name: "Failure inbox" }),
  ).toContainText("No failures match");
  await expect(
    page.getByRole("button", { name: "Filters (1)", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(50);
  await expect(
    page.getByRole("alert", { name: "Check your filters", exact: true }),
  ).toHaveCount(0);
});

test("activity shows investigation history without an automatic payload read", async ({
  page,
}, info) => {
  const payloadRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/payload")) payloadRequests.push(request.url());
  });
  await signIn(page);
  await status(page, "Unknown outcome");
  await page
    .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
    .click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await selected.getByRole("tab", { name: "Activity", exact: true }).click();
  await expect(
    selected.getByRole("heading", { name: "Retry history" }),
  ).toBeVisible();
  await expect(
    selected.getByText("Unknown — delivery not confirmed", { exact: true }),
  ).toBeVisible();
  await selected.getByText("Run identifiers", { exact: true }).click();
  await expect(selected.getByText(/Submission key: fixture-3/)).toBeVisible();
  expect(payloadRequests).toHaveLength(0);
  await page.screenshot({
    path: info.outputPath("console-activity.png"),
    fullPage: true,
  });
});

test("rejected credentials show sign-in guidance instead of an aborted-signal error", async ({
  page,
}) => {
  await page.goto("/");
  await page
    .getByLabel("Replay API token")
    .fill("invalid-synthetic-operator-token");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Your API key was rejected",
  );
  await expect(page.getByLabel("Replay API token")).toHaveValue("");
  await expect(page.getByText(/signal is aborted|AbortError/)).toHaveCount(0);
  await page.getByLabel("Replay API token").fill(OPS);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toBeVisible();
});

test("a rejected active session clears evidence and explains why sign-in is needed", async ({
  page,
}) => {
  await signIn(page);
  await page.route("**/v1/records?*", (route) =>
    route.fulfill({
      status: 401,
      contentType: "application/problem+json",
      body: JSON.stringify({ code: "unauthorized", detail: "invalid key" }),
    }),
  );
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Your API key was rejected",
  );
  await expect(
    page.getByRole("heading", { name: "Failure inbox" }),
  ).toHaveCount(0);
  await expect(page.getByText(/signal is aborted|AbortError/)).toHaveCount(0);
});

for (const width of [1272, 390]) {
  test(`resolution field errors preserve evidence and focus at ${width}px`, async ({
    page,
  }, info) => {
    await page.setViewportSize({ width, height: 900 });
    await signIn(page);
    await status(page, "Unknown outcome");
    await page
      .getByRole("button", { name: `Inspect ${id(3)}`, exact: true })
      .click();
    await page.getByRole("button", { name: "Confirm outcome" }).click();
    const note = page.getByLabel("Evidence note");
    const reference = page.getByLabel("Receipt or incident reference");
    await reference.fill("x".repeat(1025));
    await page.getByRole("button", { name: "Record confirmation" }).click();
    await expect(note).toHaveAttribute("aria-invalid", "true");
    await expect(reference).toHaveAttribute("aria-invalid", "true");
    await expect(note).toBeFocused();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
      "Check the highlighted fields",
    );
    await expect(reference).toHaveValue("x".repeat(1025));
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: info.outputPath(`validation-${width}.png`),
      fullPage: true,
      animations: "disabled",
    });
    await note.fill("Confirmed receipt in destination audit");
    await reference.fill("incident-42");
    await page.getByRole("button", { name: "Record confirmation" }).click();
    await expect(page.getByRole("dialog").getByRole("alert")).toContainText(
      "Resolved — operator confirmed",
    );
  });
}

test("inbox service errors stay readable and preserve previously loaded records", async ({
  page,
}, info) => {
  await signIn(page);
  await page.route("**/v1/records?*", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/problem+json",
      body: JSON.stringify({
        code: "unavailable",
        detail: "private database connection error",
      }),
    }),
  );
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Check service health");
  await expect(page.getByRole("alert")).toContainText(
    "Previously loaded data may be stale",
  );
  await expect(page.getByRole("button", { name: /^Inspect / })).toHaveCount(5);
  await expect(page.getByText("private database connection error")).toHaveCount(
    0,
  );
  await page.screenshot({
    path: info.outputPath("inbox-service-error.png"),
    fullPage: true,
    animations: "disabled",
  });
});

test("dismissing an unknown failure preserves delivery evidence and disables recovery", async ({ page, request }) => {
  await signIn(page);
  await status(page, "Unknown outcome");
  await page.getByRole("button", { name: `Inspect ${id(3)}`, exact: true }).click();
  const selected = page.getByRole("region", { name: "Selected failure" });
  await selected.getByRole("button", { name: "Dismiss failure", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Dismiss failure", exact: true });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Dismiss and delete artifacts", exact: true }).click();
  await expect(dialog).toContainText("Dismissed");
  await dialog.getByRole("button", { name: "Done", exact: true }).click();
  await expect(selected.locator(".delivery-state")).toHaveText("Dismissed");
  await expect(selected.getByRole("button", { name: "Retry delivery", exact: true })).toBeDisabled();
  await expect(selected.getByRole("button", { name: "Confirm outcome", exact: true })).toHaveCount(0);
  await expect(selected).toContainText("Last delivery state");
  await expect(selected.locator(".delivery-metadata")).toContainText("Unknown outcome");
  await selected.getByText("Delivery guidance", { exact: true }).click();
  await expect(selected).toContainText("Recovery was stopped");
  const record = await detail(request, 3);
  expect(record.delivery_status).toBe("unknown");
  expect(record.dismissed_at).toBeTruthy();
  expect(record.runs[0].status).toBe("unknown");
  expect(record.artifacts.stored).toBe(false);
  await page.getByRole("tab", { name: "Dismissed", exact: true }).click();
  await expect(page.getByRole("button", { name: `Inspect ${id(3)}`, exact: true })).toBeVisible();
});
