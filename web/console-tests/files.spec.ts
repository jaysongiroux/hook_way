import { expect, test, type Page } from "@playwright/test";
import { readFile, mkdir } from "node:fs/promises";
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
async function inspect(page: Page, n: number) {
  await page.getByRole("tab", { name: "Inspections", exact: true }).click();
  await page.getByRole("button", { name: `Inspect ${id(n)}`, exact: true }).click();
  await expect(page.getByRole("region", { name: "Selected inspection" })).toBeVisible();
}
async function download(page: Page, filename: string) {
  const pending = page.waitForEvent("download");
  await page.getByRole("button", { name: `Download ${filename}`, exact: true }).click();
  const file = await pending;
  expect(file.suggestedFilename()).toBe(filename);
  const bytes = await readFile((await file.path())!);
  await file.delete();
  expect(page.context().pages()).toHaveLength(1);
  return bytes;
}
test.beforeEach(async ({ request }) => { expect((await request.post(`${API}/test/reset`)).status()).toBe(204); });

test("multipart inspections show file information and download individual binary and empty files", async ({ page }) => {
  await login(page); await inspect(page, 209);
  const files = page.getByRole("region", { name: "Captured files" });
  await expect(files.getByRole("button", { name: "Download receipt.pdf" })).toHaveCount(0);
  await files.getByRole("button", { name: "Load file information" }).click();
  const pdf = page.getByRole("region", { name: "File document", exact: true });
  await expect(pdf).toContainText("receipt.pdf");
  await expect(pdf).toContainText("application/pdf");
  await expect(pdf).toContainText("4 bytes");
  await expect(files).not.toContainText("AAEC/w==");
  expect(await download(page, "receipt.pdf")).toEqual(Buffer.from([0, 1, 2, 255]));
  expect(await download(page, "second receipt.pdf")).toHaveLength(0);
  const omitted = page.getByRole("region", { name: "File notes", exact: true });
  await expect(omitted).toContainText("notes.txt");
  await expect(omitted).toContainText("12 bytes");
  await expect(omitted).toContainText("omitted by the flow configuration");
  await expect(omitted.getByRole("button")).toHaveCount(0);
  await files.getByRole("button", { name: "Close files" }).click();
  await expect(files.getByRole("button", { name: "Load file information" })).toBeVisible();
});

test("file downloads are complete even above the inspection text-preview limit", async ({ page }) => {
  await login(page); await inspect(page, 210);
  await page.getByRole("button", { name: "Load file information" }).click();
  await expect(page.getByRole("region", { name: "File large", exact: true })).toContainText("921,600 bytes");
  const bytes = await download(page, "large.txt");
  expect(bytes).toEqual(Buffer.alloc(900 * 1024, "x"));
});

test("binary inspections and direct failure payloads save the original bytes", async ({ page }) => {
  await login(page); await inspect(page, 202);
  await page.getByRole("button", { name: "Load file information" }).click();
  expect(await download(page, "body.bin")).toEqual(Buffer.from([0, 1, 2, 255]));
  await page.getByRole("tab", { name: "Failures", exact: true }).click();
  await page.getByRole("button", { name: `Inspect ${id(8)}`, exact: true }).click();
  await page.getByRole("button", { name: "Load file information" }).click();
  await expect(page.getByRole("region", { name: "Captured files" })).toContainText("original.bin");
  expect(await download(page, "original.bin")).toEqual(Buffer.from([0, 1, 2, 255]));
});

test("multipart failure envelopes support the same individual downloads", async ({ page }) => {
  await login(page);
  await page.getByLabel("Flow", { exact: true }).fill("archive");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await page.getByRole("button", { name: "Next page" }).click();
  await page.getByRole("button", { name: `Inspect ${id(68)}`, exact: true }).click();
  await page.getByRole("button", { name: "Load file information" }).click();
  expect(await download(page, "receipt.pdf")).toEqual(Buffer.from([0, 1, 2, 255]));
});

test("read-only keys never load or expose captured filenames or file contents", async ({ page }) => {
  let requests = 0;
  page.on("request", (r) => { if (r.url().endsWith("/payload")) requests++; });
  await login(page, READ); await inspect(page, 209);
  await expect(page.getByRole("button", { name: "Load file information" })).toHaveCount(0);
  await expect(page.getByText("receipt.pdf", { exact: true })).toHaveCount(0);
  expect(requests).toBe(0);
});

for (const [label, width, height] of [["desktop", 1366, 768], ["mobile", 390, 844]] as const) {
  test(`file information and downloads fit ${label}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await login(page); await inspect(page, 209);
    await page.getByRole("button", { name: "Load file information" }).click();
    const files = page.getByRole("region", { name: "Captured files" });
    await files.scrollIntoViewIfNeeded();
    await expect(files.getByRole("button", { name: "Download receipt.pdf" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const directory = resolve("../.impeccable/review");
    await mkdir(directory, { recursive: true });
    await page.screenshot({ path: `${directory}/files-${label}.png`, fullPage: true, animations: "disabled" });
  });
}
