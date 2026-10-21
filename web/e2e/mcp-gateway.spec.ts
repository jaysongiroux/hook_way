import { expect, test } from "@playwright/test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { execFile, spawn } from "node:child_process";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { BuilderPage } from "./builder";

// Compile separately so a cold Go cache cannot consume the server-start deadline.
const liveTest = test.extend<{ gatewayBinary: string }>({
  gatewayBinary: [
    async ({}, use) => {
      const directory = mkdtempSync(join(tmpdir(), "hookway-mcp-binary-"));
      const binary = join(directory, "mcp-browser");
      try {
        await promisify(execFile)(
          "go",
          ["build", "-o", binary, "./internal/httpapi/testdata/mcp-browser"],
          {
            cwd: fileURLToPath(new URL("../../", import.meta.url)),
            timeout: 180_000,
          },
        );
        await use(binary);
      } finally {
        rmSync(directory, { recursive: true, force: true });
      }
    },
    { timeout: 180_000 },
  ],
});

const fixture = readFileSync(
  new URL("../../examples/templates/mcp-gateway.yaml", import.meta.url),
  "utf8",
);
test("one MCP trigger exposes named native branches and survives YAML persistence", async ({
  page,
}) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(fixture);
  await page.getByRole("tab", { name: "Templates", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Add Authenticated MCP gateway" }),
  ).toBeEnabled();
  const trigger = page
    .locator(".react-flow__node")
    .filter({ has: page.getByText("mcp", { exact: true }) });
  await expect(
    trigger.getByLabel("get_service_status", { exact: true }),
  ).toBeVisible();
  await expect(
    trigger.getByLabel("format_greeting", { exact: true }),
  ).toBeVisible();
  await trigger.click();
  await builder.openInspector();
  await expect(
    builder.inspector().getByRole("tab", { name: "Endpoint", exact: true }),
  ).toBeVisible();
  await builder.inspector().getByRole("tab", { name: "Tools (2)" }).click();
  await expect(builder.inspector().getByLabel("Tool name").first()).toHaveValue(
    "get_service_status",
  );
  await builder.closeInspector();
  const dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
  await expect(
    dialog.getByRole("button", { name: "Download core.yaml" }),
  ).toBeEnabled();
  await expect(dialog.getByTestId("export-core.yaml")).toContainText(
    "mcp:get_service_status>status",
  );
  await builder.closeDialog();
  await page.reload();
  await expect(
    page
      .locator(".react-flow__node")
      .filter({ has: page.getByText("mcp", { exact: true }) })
      .getByLabel("format_greeting", { exact: true }),
  ).toBeVisible();
});

liveTest("live tester discovers and invokes authenticated Go gateway tools", async ({
  page,
  gatewayBinary,
}, testInfo) => {
  test.setTimeout(120_000);
  const directory = mkdtempSync(join(tmpdir(), "hookway-mcp-browser-"));
  const token = "browser-reader-012345678901234567890123456789";
  let calls = 0;
  const upstream = createServer((request, response) => {
    calls++;
    expect(request.headers.authorization).toBe("Bearer private-service-token");
    response.setHeader("Content-Type", "application/json");
    response.end(JSON.stringify({ status: "operational" }));
  });
  await new Promise<void>((resolve) =>
    upstream.listen(0, "127.0.0.1", resolve),
  );
  const address = upstream.address();
  if (!address || typeof address === "string")
    throw new Error("Missing upstream listener");
  const origin = `http://127.0.0.1:${address.port}`;
  const config = fixture
    .replaceAll("https://status.example.com", origin)
    .replace("kind: static", "kind: static\n    allow_private_targets: true")
    .replace("method: GET", "method: GET\n        allow_private_targets: true")
    .replace(
      "public_url: https://hookway.example.com/mcp",
      `public_url: http://localhost/mcp\n        allowed_origins: [${new URL(testInfo.project.use.baseURL as string).origin}]`,
    );
  const configPath = join(directory, "core.yaml");
  writeFileSync(configPath, config);
  const process = spawn(
    gatewayBinary,
    [configPath],
    {
      cwd: fileURLToPath(new URL("../../", import.meta.url)),
      detached: true,
      env: {
        ...globalThis.process.env,
        MCP_READER_TOKEN: token,
        STATUS_API_TOKEN: "private-service-token",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  try {
    const url = await new Promise<string>((resolve, reject) => {
      let stderr = "";
      const timer = setTimeout(
        () => reject(new Error(`Gateway startup timed out: ${stderr}`)),
        60_000,
      );
      process.stderr.on("data", (chunk: Buffer) => {
        stderr += chunk.toString();
      });
      process.stdout.on("data", (chunk: Buffer) => {
        const value = chunk.toString().trim();
        if (value.startsWith("http://")) {
          clearTimeout(timer);
          resolve(value);
        }
      });
      process.once("exit", (code) => {
        clearTimeout(timer);
        reject(new Error(`Gateway exited ${code}: ${stderr}`));
      });
      process.once("error", (error) => {
        clearTimeout(timer);
        reject(error);
      });
    });
    const builder = new BuilderPage(page);
    await builder.gotoReady();
    await builder.importYaml(config);
    await page
      .locator(".react-flow__node")
      .filter({ has: page.getByText("mcp", { exact: true }) })
      .click();
    await builder.openInspector();
    const inspector = builder.inspector();
    await inspector.getByRole("tab", { name: "Test & connect" }).click();
    await inspector.getByLabel("Deployed MCP URL").fill(`${url}/mcp`);
    await inspector.getByLabel("API key or OAuth access token").fill(token);
    await inspector
      .getByRole("button", { name: "Connect and discover" })
      .click();
    await expect(inspector.getByRole("status")).toHaveText(
      "Connected. 2 permitted tools.",
    );
    await inspector.getByLabel("Live tool arguments").fill('{"name":"Sam"}');
    await inspector.getByRole("button", { name: "Run live tool" }).click();
    await expect(inspector.locator("pre")).toContainText('"name": "Sam"');
    expect(calls).toBe(0);
    await inspector.getByRole("combobox", { name: "Deployed tool" }).click();
    await page
      .getByRole("option", { name: "get_service_status", exact: true })
      .click();
    await inspector.getByRole("button", { name: "Run live tool" }).click();
    await expect(inspector.locator("pre")).toContainText(
      '"status": "operational"',
    );
    expect(calls).toBe(1);
    await inspector.getByLabel("Live tool arguments").fill("{invalid");
    await expect(
      inspector.getByRole("button", { name: "Run live tool" }),
    ).toBeDisabled();
    await inspector.getByLabel("Live tool arguments").fill("{}");
    await page.screenshot({ path: testInfo.outputPath("mcp-live.png") });
    await inspector
      .getByRole("button", { name: "Disconnect and clear" })
      .click();
    await expect(
      inspector.getByLabel("API key or OAuth access token"),
    ).toHaveValue("");
  } finally {
    if (process.pid) {
      try {
        globalThis.process.kill(-process.pid, "SIGTERM");
      } catch {
        /* already exited */
      }
    }
    await new Promise<void>((resolve, reject) =>
      upstream.close((error) => (error ? reject(error) : resolve())),
    );
    rmSync(directory, { recursive: true, force: true });
  }
});

test("multiple MCP endpoints keep editable paths through export and persistence", async ({
  page,
}) => {
  const builder = new BuilderPage(page);
  await builder.gotoReady();
  await builder.importYaml(
    readFileSync(
      new URL("../../examples/mcp-endpoints.yaml", import.meta.url),
      "utf8",
    ),
  );
  const trigger = page
    .locator(".react-flow__node")
    .filter({ has: page.getByText("mcp", { exact: true }) });
  await trigger.click();
  await builder.openInspector();
  const inspector = builder.inspector();
  const path = inspector.getByLabel("Endpoint path", { exact: true });
  const previous = await path.inputValue();
  const next = previous.replace("/mcp", "/custom/mcp");
  await path.fill(next);
  await inspector
    .getByLabel(/Public URL/i)
    .fill(`https://hookway.example.com${next}`);
  await builder.closeInspector();
  const dialog = await builder.openConfigItem("Export YAML");
  await expect(dialog.getByTestId("validation-errors")).toHaveCount(0);
  await expect(dialog.getByTestId("export-core.yaml")).toContainText(next);
  await expect(dialog.getByTestId("export-core.yaml")).toContainText(
    previous.startsWith("/services") ? "/utilities/mcp" : "/services/mcp",
  );
  await builder.closeDialog();
  await page.reload();
  await page
    .locator(".react-flow__node")
    .filter({ has: page.getByText("mcp", { exact: true }) })
    .click();
  await builder.openInspector();
  await expect(
    builder.inspector().getByLabel("Endpoint path", { exact: true }),
  ).toHaveValue(next);
});
