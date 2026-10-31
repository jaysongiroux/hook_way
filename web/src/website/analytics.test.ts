import { beforeEach, expect, test, vi } from "vitest";
const sdk = vi.hoisted(() => ({
  init: vi.fn(),
  capture: vi.fn(),
  opt_in_capturing: vi.fn(),
  opt_out_capturing: vi.fn(),
  reset: vi.fn(),
}));
vi.mock("posthog-js", () => ({ default: sdk }));
beforeEach(() => {
  vi.resetModules();
  vi.clearAllMocks();
  vi.stubEnv("VITE_POSTHOG_ENABLED", "true");
  vi.stubEnv("VITE_POSTHOG_PROJECT_TOKEN", "test-project-token");
  vi.stubEnv("VITE_POSTHOG_HOST", "https://ingest.example.com");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true }));
  localStorage.clear();
});
test("does not initialize or capture before consent, and stops immediately after decline", async () => {
  const analytics = await import("./analytics");
  analytics.capture("github_opened", "/");
  expect(sdk.init).not.toHaveBeenCalled();
  expect(sdk.capture).not.toHaveBeenCalled();
  await analytics.allowAnalytics();
  analytics.capture("github_opened", "/");
  expect(sdk.init).toHaveBeenCalledTimes(1);
  expect(sdk.capture).toHaveBeenCalledTimes(1);
  analytics.declineAnalytics();
  analytics.capture("github_opened", "/");
  expect(sdk.capture).toHaveBeenCalledTimes(1);
  expect(sdk.opt_out_capturing).toHaveBeenCalled();
}, 15000);
test("event payloads discard editor data and raw initial, session, referrer and current URLs", async () => {
  const analytics = await import("./analytics");
  await analytics.allowAnalytics();
  const clean = analytics.sanitizeEvent({
    event: "$pageview",
    properties: {
      page_path: "/builder",
      page_category: "builder",
      method: "download",
      config: "SECRET_MARKER",
      utm_source: "SECRET_MARKER",
      location: "SECRET_MARKER",
      landing_page: "/private/SECRET_MARKER",
      $current_url: "https://site/?token=SECRET_MARKER",
      $initial_current_url: "https://site/#SECRET_MARKER",
      $session_entry_url: "https://site/?SECRET_MARKER",
      $referrer: "https://private/?SECRET_MARKER",
      $set: { payload: "SECRET_MARKER" },
    },
    $set: { payload: "SECRET_MARKER" },
  });
  expect(clean).not.toBeNull();
  expect(JSON.stringify(clean)).not.toContain("SECRET_MARKER");
  expect(clean?.properties.$current_url).toBe(
    "https://www.gethookway.com/builder",
  );
  expect(
    analytics.sanitizeEvent({
      event: "$autocapture",
      properties: { page_path: "/" },
    }),
  ).toBeNull();
  expect(analytics.safePagePath("/private/SECRET_MARKER")).toBeNull();
});
test("blocked storage does not break preference changes", async () => {
  const analytics = await import("./analytics");
  const read = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw Error("blocked");
  });
  const write = vi
    .spyOn(Storage.prototype, "setItem")
    .mockImplementation(() => {
      throw Error("blocked");
    });
  expect(analytics.readConsent()).toBeNull();
  expect(() => analytics.writeConsent("decline")).not.toThrow();
  read.mockRestore();
  write.mockRestore();
});

test("delivery aborts pending requests on withdrawal and never queues retries", async () => {
  const analytics = await import("./analytics");
  await analytics.allowAnalytics();
  const request = {
    event: "$pageview",
    properties: {
      page_path: "/",
      distinct_id: "anonymous-test",
      $process_person_profile: false,
    },
  };
  expect(analytics.deliverEvent(request)).toBeNull();
  expect(fetch).toHaveBeenCalledTimes(1);
  const options = vi.mocked(fetch).mock.calls[0][1]!;
  expect(options.referrerPolicy).toBe("no-referrer");
  expect(options.signal!.aborted).toBe(false);
  analytics.declineAnalytics();
  expect(options.signal!.aborted).toBe(true);
  analytics.deliverEvent(request);
  expect(fetch).toHaveBeenCalledTimes(1);
});
