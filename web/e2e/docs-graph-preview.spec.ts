import { expect, test, type Page } from "@playwright/test";
import { BuilderPage } from "./builder";

const preview = (page: Page) => page.getByRole("region", { name: "Example graph preview" });
const node = (page: Page, id: string) => preview(page).getByRole("button", { name: new RegExp("^" + id + " \\(") });

for (const width of [1440, 390]) {
  test(`example graph and read-only node details fit at ${width}px`, async ({ page }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/docs/examples/auth/no_auth/");
    await expect(node(page, "out")).toBeVisible();
    await expect(preview(page).getByText("Send to RabbitMQ")).toBeVisible();
    expect(await preview(page).evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    const graphBounds = await preview(page).boundingBox();
    const yamlBounds = await page.locator(".docs-content").boundingBox();
    expect(graphBounds!.y + graphBounds!.height).toBeLessThan(yamlBounds!.y);
    await node(page, "out").click();
    const details = page.getByRole("dialog", { name: "Node details: out" });
    await expect(details).toContainText("Destination: primary");
    await expect(details).toContainText("${RABBITMQ_URL}");
    await expect(details.locator("input, textarea, select, [contenteditable=true]")).toHaveCount(0);
    expect(await details.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await page.screenshot({ animations: "disabled", path: testInfo.outputPath("node-details-" + width + ".png") });
    await page.keyboard.press("Escape");
    await expect(details).toBeHidden();
    await expect(node(page, "out")).toBeFocused();
    await node(page, "in").focus();
    await page.keyboard.press("Enter");
    await expect(page.getByRole("dialog", { name: "Node details: in" })).toContainText("/webhooks/internal");
    await page.keyboard.press("Escape");
    await page.keyboard.press("Space");
    await expect(page.getByRole("dialog", { name: "Node details: in" })).toBeVisible();
    await page.keyboard.press("Escape");
    await page.screenshot({ animations: "disabled", path: testInfo.outputPath("graph-preview-" + width + ".png") });
    expect(errors).toEqual([]);
  });
}

test("pan, zoom, and fit work while drag, delete, wiring, and drops cannot edit the graph", async ({ page }) => {
  await page.goto("/docs/examples/auth/no_auth/");
  await expect(node(page, "in")).toBeVisible();
  const graph = preview(page);
  const viewport = graph.locator(".react-flow__viewport");
  const transform = () => viewport.evaluate((element) => getComputedStyle(element).transform);
  const original = await transform();
  await graph.getByRole("button", { name: "Zoom In", exact: true }).click();
  await expect.poll(transform).not.toBe(original);
  await graph.getByRole("button", { name: "Fit View", exact: true }).click();
  await expect.poll(transform).toBe(original);
  await graph.hover({ position: { x: 60, y: 100 } });
  await page.mouse.wheel(120, 100);
  await expect.poll(transform).not.toBe(original);
  await graph.getByRole("button", { name: "Fit View", exact: true }).click();
  await expect.poll(transform).toBe(original);

  const positions = () => graph.locator(".react-flow__node").evaluateAll((elements) => elements.map((element) => (element as HTMLElement).style.transform));
  const before = await positions();
  const first = node(page, "in");
  await first.focus();
  await page.keyboard.press("Delete");
  await page.keyboard.press("Backspace");
  await page.keyboard.press("ArrowRight");
  const bounds = await first.boundingBox();
  await page.mouse.move(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2);
  await page.mouse.down();
  await page.mouse.move(bounds!.x + bounds!.width / 2 + 80, bounds!.y + bounds!.height / 2 + 60, { steps: 8 });
  await page.mouse.up();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await graph.getByRole("button", { name: "Fit View", exact: true }).click();
  const source = graph.locator('[data-id="in"] .source');
  const target = graph.locator('[data-id="out"] .target');
  await expect(source).not.toHaveClass(/connectablestart/);
  // Read-only ports deliberately do not receive pointer events. Attempt wiring
  // with real pointer coordinates, including checking while the drag is active.
  const sourceBounds = (await source.boundingBox())!;
  const targetBounds = (await target.boundingBox())!;
  await page.mouse.move(sourceBounds.x + sourceBounds.width / 2, sourceBounds.y + sourceBounds.height / 2);
  await page.mouse.down();
  await page.mouse.move(targetBounds.x + targetBounds.width / 2, targetBounds.y + targetBounds.height / 2, { steps: 8 });
  await expect(graph.locator(".react-flow__connection")).toHaveCount(0);
  await page.mouse.up();
  await expect(graph.locator(".react-flow__edge")).toHaveCount(4);
  const transfer = await page.evaluateHandle(() => {
    const data = new DataTransfer();
    data.setData("application/hookway-node", "respond");
    data.setData("application/hookway-template", "slack");
    return data;
  });
  await graph.dispatchEvent("drop", { dataTransfer: transfer });
  await transfer.dispose();
  await expect(graph.locator(".react-flow__node")).toHaveCount(5);
  expect(await positions()).toEqual(before);
  await expect(graph.getByRole("button", { name: "Arrange nodes" })).toHaveCount(0);
  await expect(graph.getByRole("button", { name: /interactivity/i })).toHaveCount(0);
});

test("split examples switch flows and standalone flow pages also preview", async ({ page }) => {
  await page.goto("/docs/examples/multi-yaml/core/");
  await expect(preview(page).getByLabel("Flow", { exact: true })).toHaveValue("orders");
  await node(page, "reply").click();
  await expect(page.getByRole("dialog")).toContainText("accepted orders");
  await page.keyboard.press("Escape");
  await preview(page).getByLabel("Flow", { exact: true }).selectOption("billing");
  await expect(preview(page).getByText("/billing", { exact: true })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await node(page, "reply").click();
  await expect(page.getByRole("dialog")).toContainText("accepted billing");
  await page.keyboard.press("Escape");
  // Navigate through docs links to exercise cleanup without a browser reload.
  const navigation = page.getByRole("navigation", { name: "Documentation", exact: true });
  await navigation.locator("summary").filter({ hasText: /^Flows$/ }).click();
  await navigation.getByRole("link", { name: "Orders", exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/examples\/multi-yaml\/flows\/orders\/$/);
  await expect(node(page, "in")).toBeVisible();
  await expect(preview(page).getByText("/orders", { exact: true })).toBeVisible();
  await expect(preview(page).getByLabel("Flow", { exact: true })).toHaveCount(0);
});

test("non-flow YAML has no graph and download failures leave the YAML usable", async ({ page }) => {
  await page.goto("/docs/examples/docker-compose/");
  await expect(page.getByRole("link", { name: "Download YAML", exact: true })).toBeVisible();
  await expect(page.getByLabel("Loading graph preview")).toHaveCount(0);
  await expect(preview(page)).toHaveCount(0);
  await page.route("**/docs/raw/examples/auth/no_auth.yaml", (route) => route.fulfill({ status: 503, body: "Unavailable" }));
  await page.goto("/docs/examples/auth/no_auth/");
  await expect(page.getByRole("status")).toContainText("Graph preview unavailable");
  await expect(page.locator(".docs-content")).toContainText("internal_events");
  await expect(page.getByRole("button", { name: "Copy YAML", exact: true })).toBeEnabled();
});

test("inline multi-flow previews retain error routes, descriptions, and nested node configuration", async ({ page }) => {
  await page.goto("/docs/examples/on_error/");
  await expect(preview(page).getByLabel("Flow", { exact: true })).toHaveValue("github_ack_or_dlq");
  await expect(preview(page).locator(".react-flow__edgelabel-renderer").getByText("on_error", { exact: true })).toBeVisible();
  await expect(preview(page).locator('.react-flow__edge-path[marker-end]')).toHaveCount(7);
  await node(page, "ack").click();
  const details = page.getByRole("dialog", { name: "Node details: ack" });
  await expect(details).toContainText("transient failures retry");
  await expect(details).toContainText("max_attempts: 3");
  await expect(details).toContainText("initial_backoff: 500ms");
  await page.keyboard.press("Escape");
  await preview(page).getByLabel("Flow", { exact: true }).selectOption("proxy_or_502");
  await expect(node(page, "up")).toBeVisible();
  await expect(node(page, "ack")).toHaveCount(0);
});

test("viewing examples preserves the saved builder workspace even with overlapping node IDs", async ({ page }) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml("flows: {saved: {nodes: [{id: in, type: http_trigger, path: /saved}], edges: []}}\n");
  const saved = () => page.evaluate(async () => {
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open("hookway-config-ui");
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    const result = await new Promise<{ activeFlow?: string }>((resolve, reject) => {
      const request = db.transaction("graphs").objectStore("graphs").get("current.v4");
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    db.close();
    return result;
  });
  await expect.poll(async () => (await saved())?.activeFlow).toBe("saved");
  const before = await saved();
  await page.goto("/docs/examples/auth/no_auth/");
  await node(page, "in").click();
  await expect(page.getByRole("dialog")).toContainText("/webhooks/internal");
  await page.keyboard.press("Escape");
  expect(await saved()).toEqual(before);
  await page.goto("/builder");
  await expect(page.getByRole("button", { name: "Active flow" })).toHaveText("saved");
  const exported = await builder.openConfigItem("Export YAML");
  await expect(exported.getByTestId("export-core.yaml")).toContainText("/saved");
  await expect(exported.getByTestId("export-core.yaml")).not.toContainText("/webhooks/internal");
});
