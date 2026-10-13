import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./console-tests",
  forbidOnly: !!process.env.CI,
  workers: 1,
  timeout: 45_000,
  use: { baseURL: "http://127.0.0.1:5183", trace: "on-first-retry" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: [
    {
      command: "cd .. && HOOKWAY_CONSOLE_FIXTURE=1 HOOKWAY_REQUIRE_INTEGRATION=1 go test ./internal/replay/consolebrowser -run TestConsoleBrowserFixture -count=1 -timeout=30m",
      url: "http://127.0.0.1:5184/healthz",
      reuseExistingServer: false,
      timeout: 120_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
    },
    {
      command: "REPLAY_API_URL=http://127.0.0.1:5184 npm run dev:console -- --host 127.0.0.1 --port 5183 --strictPort",
      url: "http://127.0.0.1:5183/",
      reuseExistingServer: false,
      timeout: 30_000,
    },
  ],
});
