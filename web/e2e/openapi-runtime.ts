import { createServer } from "node:http";
import { promisify } from "node:util";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
export async function openAPIRuntime() {
  const directory = mkdtempSync(join(tmpdir(), "hookway-openapi-browser-"));
  const requests: {
    method: string;
    url: string;
    body: string;
    authorization?: string;
  }[] = [];
  const upstream = createServer(async (request, response) => {
    let body = "";
    for await (const chunk of request) body += String(chunk);
    requests.push({
      method: request.method!,
      url: request.url!,
      body,
      authorization: request.headers.authorization,
    });
    response.setHeader("Content-Type", "application/json");
    response.end(
      JSON.stringify({
        id: "a/b",
        name: request.method === "PATCH" ? "Updated" : "Sam",
      }),
    );
  });
  await new Promise<void>((resolve) =>
    upstream.listen(0, "127.0.0.1", resolve),
  );
  const address = upstream.address();
  if (!address || typeof address === "string")
    throw new Error("Missing upstream listener");
  let gateway: ChildProcess | undefined;
  const reader = "browser-reader-012345678901234567890123456789";
  const writer = "browser-writer-012345678901234567890123456789";
  return {
    origin: `http://127.0.0.1:${address.port}`,
    requests,
    reader,
    writer,
    async generateCLI(document: string, settings: unknown) {
      const spec = join(directory, "api.yaml"),
        options = join(directory, "settings.json");
      writeFileSync(spec, document);
      writeFileSync(options, JSON.stringify(settings));
      const result = await promisify(execFile)(
        "go",
        [
          "run",
          "./cmd/hookway",
          "import-openapi",
          "--spec",
          spec,
          "--settings",
          options,
        ],
        {
          cwd: fileURLToPath(new URL("../../", import.meta.url)),
          maxBuffer: 16 * 1024 * 1024,
          timeout: 60000,
        },
      );
      return result.stdout;
    },
    async start(config: string) {
      const path = join(directory, "core.yaml");
      writeFileSync(path, config);
      gateway = spawn(
        "go",
        ["run", "./internal/httpapi/testdata/mcp-browser", path],
        {
          cwd: fileURLToPath(new URL("../../", import.meta.url)),
          detached: true,
          env: {
            ...process.env,
            MCP_READER_TOKEN: reader,
            MCP_WRITER_TOKEN: writer,
            UNRELATED_HOST_SECRET: "must-not-appear",
          },
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      return new Promise<string>((resolve, reject) => {
        let stderr = "",
          stdout = "";
        const timer = setTimeout(
          () => reject(new Error(`Gateway startup timed out: ${stderr}`)),
          60000,
        );
        gateway!.stderr!.on("data", (chunk) => {
          stderr += String(chunk);
        });
        gateway!.stdout!.on("data", (chunk) => {
          stdout += String(chunk);
          const url = stdout
            .split("\n")
            .find((line) => line.startsWith("http://"));
          if (url) {
            clearTimeout(timer);
            resolve(url.trim());
          }
        });
        gateway!.once("exit", (code) => {
          clearTimeout(timer);
          reject(new Error(`Gateway exited ${code}: ${stderr}`));
        });
        gateway!.once("error", (error) => {
          clearTimeout(timer);
          reject(error);
        });
      });
    },
    async close() {
      if (gateway?.pid) {
        try {
          process.kill(-gateway.pid, "SIGTERM");
        } catch {
          /* already exited */
        }
      }
      upstream.closeAllConnections();
      await new Promise<void>((resolve, reject) =>
        upstream.close((error) => (error ? reject(error) : resolve())),
      );
      rmSync(directory, { recursive: true, force: true });
    },
  };
}
