import { expect, test, type Locator } from "@playwright/test";
import { readFileSync } from "node:fs";
import { openAPIRuntime } from "./openapi-runtime";
import { BuilderPage } from "./builder";
test.use({ actionTimeout: 15_000 });
const source = readFileSync(
  new URL("../../examples/openapi/api.yaml", import.meta.url),
  "utf8",
);
async function pick(
  page: import("@playwright/test").Page,
  scope: Locator,
  label: string,
  name: string,
) {
  await scope.getByRole("combobox", { name: label, exact: true }).click();
  await page.getByRole("option", { name, exact: true }).click();
}
test("real WASM imports a standalone configuration without touching the current workspace", async ({
  page,
}, testInfo) => {
  test.setTimeout(120_000);
  const runtime = await openAPIRuntime();
  try {
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    const before = await page.locator(".react-flow__node").count();
    const wizard = await builder.openConfigItem(
      "Create MCP tools from OpenAPI",
    );
    await wizard.getByRole("tab", { name: "Paste document" }).click();
    await wizard.getByLabel("OpenAPI document", { exact: true }).fill(source);
    await wizard.getByRole("button", { name: "Find operations" }).click();
    await expect(
      wizard.getByRole("checkbox", {
        name: "GET /customers/{id}",
        exact: true,
      }),
    ).toBeVisible({ timeout: 45_000 });
    await expect(
      wizard.getByRole("button", { name: "Configure access", exact: true }),
    ).toBeDisabled();
    await wizard
      .getByRole("checkbox", { name: "GET /customers/{id}", exact: true })
      .check();
    await wizard
      .getByRole("checkbox", { name: "PATCH /customers/{id}", exact: true })
      .check();
    await wizard
      .getByRole("button", { name: "Edit tool details" })
      .nth(0)
      .click();
    await wizard
      .getByRole("button", { name: "Edit tool details" })
      .nth(1)
      .click();
    await wizard
      .getByLabel("Description for GET /customers/{id}", { exact: true })
      .fill("Literal ${UNRELATED_HOST_SECRET}");
    const writeDescription = await wizard
      .getByLabel("Description for PATCH /customers/{id}", { exact: true })
      .inputValue();
    await wizard
      .getByRole("button", { name: "Configure access", exact: true })
      .click();
    await wizard
      .getByLabel("Upstream base URL", { exact: true })
      .fill(`${runtime.origin}/v1`);
    await wizard
      .getByRole("switch", {
        name: "Allow private upstream targets",
        exact: true,
      })
      .check();
    await wizard.getByRole("button", { name: "Set agent access" }).click();
    await wizard
      .getByLabel("Public MCP URL", { exact: true })
      .fill("http://localhost/mcp");
    for (const caller of ["reader", "writer"]) {
      await wizard.getByLabel("New caller name", { exact: true }).fill(caller);
      await wizard
        .getByRole("button", { name: "Add caller", exact: true })
        .click();
      await wizard
        .getByLabel("Environment variable name", { exact: true })
        .fill(`MCP_${caller.toUpperCase()}_TOKEN`);
    }
    await wizard
      .getByRole("button", { name: "Browser origins & request limits" })
      .click();
    await wizard
      .getByRole("textbox", { name: "Allowed browser origins", exact: true })
      .fill(new URL(testInfo.project.use.baseURL as string).origin);
    await wizard.getByRole("button", { name: "Set tool permissions" }).click();
    await pick(
      page,
      wizard,
      "API key grants for GET /customers/{id}",
      "reader",
    );
    await page.keyboard.press("Escape");
    await pick(
      page,
      wizard,
      "API key grants for PATCH /customers/{id}",
      "writer",
    );
    await page.keyboard.press("Escape");
    await wizard
      .getByRole("checkbox", {
        name: "Allow mutation for PATCH /customers/{id}",
      })
      .check();
    await wizard
      .getByRole("button", { name: "Review configuration", exact: true })
      .click();
    await expect(
      wizard.getByText("Configuration validated", { exact: true }),
    ).toBeVisible({ timeout: 45000 });
    await page.screenshot({
      path: testInfo.outputPath("openapi-export-desktop.png"),
    });
    const viewport = page.viewportSize()!;
    await page.setViewportSize({ width: 414, height: 896 });
    await expect(
      wizard.getByRole("button", { name: "Download core.yaml", exact: true }),
    ).toBeInViewport();
    expect(
      await wizard.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: testInfo.outputPath("openapi-export-mobile.png"),
    });
    await page.setViewportSize(viewport);
    await wizard.getByRole("button", { name: "View core.yaml" }).click();
    await expect(wizard.getByLabel("Generated core.yaml")).toBeVisible({
      timeout: 45_000,
    });
    const generated = await wizard
      .getByLabel("Generated core.yaml")
      .inputValue();
    expect(
      await runtime.generateCLI(source, {
        server: { addr: "127.0.0.1:8080", max_body_bytes: 1048576 },
        endpoint: {
          path: "/mcp",
          public_url: "http://localhost/mcp",
          allowed_origins: [
            new URL(testInfo.project.use.baseURL as string).origin,
          ],
          auth: {
            api_keys: {
              reader: {
                secret_ref: { provider: "env", name: "MCP_READER_TOKEN" },
              },
              writer: {
                secret_ref: { provider: "env", name: "MCP_WRITER_TOKEN" },
              },
            },
          },
        },
        upstream: {
          base_url: `${runtime.origin}/v1`,
          allow_private_targets: true,
        },
        operations: [
          {
            method: "GET",
            path: "/customers/{id}",
            name: "get_customer",
            description: "Literal ${UNRELATED_HOST_SECRET}",
            access: { api_keys: ["reader"] },
          },
          {
            method: "PATCH",
            path: "/customers/{id}",
            name: "update_customer",
            description: writeDescription,
            access: { api_keys: ["writer"], allow_mutation: true },
          },
        ],
      }),
    ).toBe(generated);
    expect(generated).toContain("\\u0024{UNRELATED_HOST_SECRET}");
    const download = page.waitForEvent("download");
    await wizard
      .getByRole("button", { name: "Download core.yaml", exact: true })
      .click();
    expect(readFileSync((await (await download).path())!, "utf8")).toBe(
      generated,
    );
    expect(await page.locator(".react-flow__node").count()).toBe(before);
    const gateway = await runtime.start(generated);
    await wizard.getByRole("button", { name: /Connect & test/ }).click();
    await wizard.getByLabel("Deployed MCP URL").fill(`${gateway}/mcp`);
    await wizard
      .getByLabel("API key or OAuth access token")
      .fill(runtime.reader);
    await wizard.getByRole("button", { name: "Connect and discover" }).click();
    await expect(wizard.getByRole("status")).toHaveText(
      "Connected. 1 permitted tools.",
    );
    await wizard
      .getByLabel("Live tool arguments")
      .fill('{"path":{"id":"a/b"}}');
    await wizard.getByRole("button", { name: "Run live tool" }).click();
    await expect(
      wizard.getByRole("region", { name: "Live tool result" }),
    ).toContainText('"name": "Sam"');
    await expect(
      wizard.getByRole("combobox", { name: "Deployed tool", exact: true }),
    ).toHaveValue("get_customer");
    expect(runtime.requests).toHaveLength(1);
    expect(runtime.requests[0]).toEqual({
      method: "GET",
      url: "/v1/customers/a%2Fb",
      body: "",
      authorization: undefined,
    });
    const denied = await fetch(`${gateway}/mcp`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${runtime.reader}`,
        Accept: "application/json, text/event-stream",
        "Content-Type": "application/json",
        "MCP-Protocol-Version": "2025-11-25",
        Origin: new URL(testInfo.project.use.baseURL as string).origin,
      },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 99,
        method: "tools/call",
        params: {
          name: "update_customer",
          arguments: { path: { id: "a/b" }, body: { name: "Forbidden" } },
        },
      }),
    });
    expect(denied.status).toBe(403);
    expect(await denied.text()).toContain("Forbidden");
    expect(runtime.requests).toHaveLength(1);
    await wizard.getByRole("button", { name: "Disconnect and clear" }).click();
    await expect(
      wizard.getByLabel("API key or OAuth access token"),
    ).toHaveValue("");
    await wizard
      .getByLabel("API key or OAuth access token")
      .fill(runtime.writer);
    await wizard.getByRole("button", { name: "Connect and discover" }).click();
    await expect(wizard.getByRole("status")).toHaveText(
      "Connected. 1 permitted tools.",
    );
    await wizard
      .getByLabel("Live tool arguments")
      .fill('{"path":{"id":"a/b"},"body":{"name":"Updated"}}');
    await expect(
      wizard.getByRole("button", { name: "Run live tool" }),
    ).toBeDisabled();
    await wizard
      .getByRole("checkbox", {
        name: "I understand this live call can change upstream data",
      })
      .check();
    await wizard.getByRole("button", { name: "Run live tool" }).click();
    await expect(
      wizard.getByRole("region", { name: "Live tool result" }),
    ).toContainText('"name": "Updated"');
    expect(runtime.requests.filter((r) => r.method === "PATCH")).toEqual([
      {
        method: "PATCH",
        url: "/v1/customers/a%2Fb",
        body: '{"name":"Updated"}',
        authorization: undefined,
      },
    ]);
    await wizard.getByRole("button", { name: "Disconnect and clear" }).click();
    await wizard.getByRole("button", { name: "Back", exact: true }).click();
    await wizard
      .getByRole("button", { name: "Open in builder", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Export current workspace", exact: true })
      .click();
    const previousExport = page.getByRole("dialog", {
      name: "Export configs",
      exact: true,
    });
    await expect(previousExport).toBeVisible();
    await expect(
      previousExport.getByTestId("export-core.yaml"),
    ).not.toContainText("imported_api:");
    await previousExport.getByRole("button", { name: /close/i }).click();
    await expect(previousExport).toBeHidden();
    await page
      .getByRole("button", { name: "Cancel replacement", exact: true })
      .click();
    expect(await page.locator(".react-flow__node").count()).toBe(before);
    await wizard.getByRole("button", { name: "View core.yaml" }).click();
    await page.screenshot({ path: testInfo.outputPath("openapi-review.png") });
    await wizard
      .getByRole("button", { name: "Open in builder", exact: true })
      .click();
    await page
      .getByRole("button", { name: "Replace workspace", exact: true })
      .click();
    await expect(wizard).toBeHidden();
    await expect(page.locator(".react-flow__node")).toHaveCount(5);
    const exported = await builder.openConfigItem("Export YAML");
    await expect(exported.getByTestId("export-core.yaml")).toContainText(
      "\\u0024{UNRELATED_HOST_SECRET}",
    );
    await builder.closeDialog();
    await page.reload();
    const again = await builder.openConfigItem("Export YAML");
    const exportedText = await again
      .getByTestId("export-core.yaml")
      .innerText();
    expect(exportedText).not.toContain(runtime.reader);
    expect(exportedText).not.toContain(runtime.writer);
    expect(exportedText).not.toContain("must-not-appear");
    await expect(again.getByTestId("export-core.yaml")).toContainText(
      "\\u0024{UNRELATED_HOST_SECRET}",
    );
  } finally {
    await runtime.close();
  }
});

test("mobile upload, unsupported diagnostics and worker retry keep the canvas validator independent", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 414, height: 896 });
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  const wizard = await builder.openConfigItem("Create MCP tools from OpenAPI");
  const externalRequests: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("untrusted.invalid"))
      externalRequests.push(request.url());
  });
  await wizard.locator('input[type="file"]').setInputFiles({
    name: "too-large.yaml",
    mimeType: "application/yaml",
    buffer: Buffer.alloc(8 * 1024 * 1024 + 1, 97),
  });
  await expect(wizard.getByRole("alert")).toContainText("exceeds 8 MiB");
  await wizard.getByRole("tab", { name: "Paste document" }).click();
  await wizard
    .getByLabel("OpenAPI document", { exact: true })
    .fill(
      "openapi: 3.1.0\ninfo: {title: Test, version: '1'}\npaths: {}\ncomponents: {schemas: {Secret: {$ref: 'https://untrusted.invalid/spec.yaml'}}}",
    );
  await wizard.getByRole("button", { name: "Find operations" }).click();
  await expect(
    wizard
      .locator('[data-step="0"]')
      .getByText("only same-document JSON Pointer references are supported", {
        exact: true,
      }),
  ).toBeVisible({ timeout: 45000 });
  expect(externalRequests).toEqual([]);
  const unsupported = source.replace(
    "    get:",
    "    head:\n      responses:\n        '204': {description: Empty}\n    get:",
  );
  await wizard.locator('input[type="file"]').setInputFiles({
    name: "api.yaml",
    mimeType: "application/yaml",
    buffer: Buffer.from(unsupported),
  });
  await page.route("**/hookway.wasm", (route) =>
    route.fulfill({ status: 503, body: "unavailable" }),
  );
  await wizard.getByRole("button", { name: "Find operations" }).click();
  await expect(wizard.getByRole("alert")).toContainText(
    "WASM assets are unavailable",
  );
  await page.unroute("**/hookway.wasm");
  await wizard.getByRole("button", { name: "Find operations" }).click();
  await expect(
    wizard.getByRole("checkbox", { name: "HEAD /customers/{id}", exact: true }),
  ).toBeDisabled({ timeout: 45000 });
  await expect(
    wizard.getByRole("checkbox", { name: "GET /customers/{id}", exact: true }),
  ).not.toBeChecked();
  await wizard
    .getByRole("checkbox", { name: "GET /customers/{id}", exact: true })
    .check();
  await wizard.getByRole("button", { name: "Edit tool details" }).click();
  await wizard
    .getByRole("textbox", { name: "Tool name for GET /customers/{id}" })
    .focus();
  await page.keyboard.press("Tab");
  await expect(
    wizard.getByRole("textbox", {
      name: "Description for GET /customers/{id}",
    }),
  ).toBeFocused();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("openapi-mobile.png") });
  await wizard.getByRole("button", { name: "Close OpenAPI wizard" }).click();
  await page.getByRole("button", { name: "Discard import" }).click();
  await expect(wizard).toBeHidden();
  const exported = await builder.openConfigItem("Export YAML");
  await expect(exported.getByTestId("validation-errors")).toHaveCount(0);
  await expect(
    exported.getByRole("button", { name: /^Download / }),
  ).toBeEnabled();
});
