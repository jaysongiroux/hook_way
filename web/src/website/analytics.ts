import type { PostHog } from "posthog-js";
import links, { site } from "virtual:hookway-docs";
import registry from "../../site.json";
import { canonicalPath } from "./metadata";
import type { TelemetryEvent, TelemetryProperties } from "../shared/telemetry";

export const CONSENT_KEY = "hookway.analytics-consent.v1";
export type Consent = "allow" | "decline" | null;
export const analyticsEnabled =
  import.meta.env.VITE_POSTHOG_ENABLED === "true" &&
  Boolean(
    import.meta.env.VITE_POSTHOG_PROJECT_TOKEN &&
      import.meta.env.VITE_POSTHOG_HOST,
  );
const paths = new Set([
  "/builder",
  ...registry.pages.map((p) => p.path),
  ...Object.values(links),
]);
const events = new Set([
  "$pageview",
  "builder_opened",
  "quick_start_copied",
  "config_exported",
  "github_opened",
]);
const propertyKeys = new Set([
  "token",
  "distinct_id",
  "$device_id",
  "$session_id",
  "$window_id",
  "$lib",
  "$lib_version",
  "$insert_id",
  "$time",
  "$timestamp",
  "$process_person_profile",
  "page_path",
  "page_category",
  "referrer_host",
  "landing_page",
  "utm_source",
  "utm_medium",
  "utm_campaign",
  "location",
  "method",
  "format",
]);
let client: PostHog | undefined;
let loading: Promise<PostHog | undefined> | undefined;
let permitted = false;
let delivery: AbortController | undefined;
const boundedProperties: Record<string, readonly string[]> = {
  page_category: ["marketing", "docs", "builder"],
  location: ["header", "footer", "hero", "quick_start", "builder"],
  method: ["copy", "download"],
  format: ["yaml", "zip"],
  utm_source: [
    "google",
    "chatgpt",
    "perplexity",
    "github",
    "hackernews",
    "reddit",
    "linkedin",
    "newsletter",
  ],
  utm_medium: ["organic", "referral", "social", "email"],
  utm_campaign: ["launch", "show-hn", "selfhosted"],
};
let landingPage: string | null = null;

export const safePagePath = (path: string) => {
  const value = canonicalPath(path);
  return paths.has(value) ? value : null;
};
export function readConsent(): Consent {
  try {
    const value = localStorage.getItem(CONSENT_KEY);
    return value === "allow" || value === "decline" ? value : null;
  } catch {
    return null;
  }
}
export function writeConsent(consent: Exclude<Consent, null>) {
  try {
    localStorage.setItem(CONSENT_KEY, consent);
  } catch {
    /* Current page choice still applies. */
  }
}
export function sanitizeEvent<
  T extends { event: string; properties: Record<string, unknown> },
>(event: T): T | null {
  if (!permitted || !events.has(event.event)) return null;
  const page = safePagePath(String(event.properties.page_path || ""));
  if (!page) return null;
  const properties = Object.fromEntries(
    Object.entries(event.properties).filter(([key]) => propertyKeys.has(key)),
  );
  for (const [key, values] of Object.entries(boundedProperties)) {
    if (!values.includes(String(properties[key]))) delete properties[key];
  }
  if (!safePagePath(String(properties.landing_page || "")))
    delete properties.landing_page;
  properties.$current_url = site.origin + page;
  properties.$pathname = page;
  const clean = { ...event, properties };
  for (const key of Object.keys(clean)) {
    if (!["event", "properties", "timestamp", "uuid"].includes(key))
      delete (clean as Record<string, unknown>)[key];
  }
  return clean;
}
// The SDK retains accepted batches/retries after opt-out. Keep delivery
// cancellable and unqueued through its public before_send hook and Capture API.
// https://github.com/PostHog/posthog-js/issues/5020
export function deliverEvent<
  T extends { event: string; properties: Record<string, unknown> },
>(event: T): null {
  const clean = sanitizeEvent(event);
  if (!clean || !delivery || !analyticsEnabled) return null;
  try {
    void fetch(
      import.meta.env.VITE_POSTHOG_HOST.replace(/\/$/, "") + "/i/v0/e/",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "omit",
        referrerPolicy: "no-referrer",
        keepalive: true,
        signal: delivery.signal,
        body: JSON.stringify({
          ...clean,
          properties: {
            ...clean.properties,
            $process_person_profile: false,
            $geoip_disable: true,
          },
          api_key: import.meta.env.VITE_POSTHOG_PROJECT_TOKEN,
          distinct_id: clean.properties.distinct_id,
        }),
      },
    ).catch(() => {
      /* Best effort. Do not retry after consent changes. */
    });
  } catch {
    /* Analytics must never interrupt the action. */
  }
  return null; // Prevent the SDK from also queuing or sending this event.
}
export async function allowAnalytics() {
  permitted = true;
  if (!analyticsEnabled) return;
  delivery ??= new AbortController();
  if (client) {
    try {
      client.opt_in_capturing({ captureEventName: false });
      return client;
    } catch {
      return undefined;
    }
  }
  loading ??= import("posthog-js")
    .then(({ default: posthog }) => {
      if (!permitted) return undefined;
      posthog.init(import.meta.env.VITE_POSTHOG_PROJECT_TOKEN, {
        api_host: import.meta.env.VITE_POSTHOG_HOST,
        defaults: "2026-08-30",
        autocapture: false,
        capture_pageview: false,
        capture_pageleave: false,
        disable_session_recording: true,
        capture_exceptions: false,
        capture_webmcp: false,
        person_profiles: "never",
        persistence: "localStorage",
        save_referrer: false,
        save_campaign_params: false,
        get_current_url: () =>
          site.origin + (safePagePath(location.pathname) || "/"),
        advanced_disable_flags: true,
        capture_dead_clicks: false,
        capture_heatmaps: false,
        capture_performance: false,
        request_batching: false,
        disable_surveys: true,
        disable_external_dependency_loading: true,
        before_send: (event) => (event ? deliverEvent(event) : null),
      });
      client = posthog;
      return client;
    })
    .catch(() => undefined)
    .finally(() => {
      loading = undefined;
    });
  return loading;
}
export function declineAnalytics() {
  permitted = false;
  landingPage = null;
  delivery?.abort();
  delivery = undefined;
  try {
    client?.opt_out_capturing();
    client?.reset();
  } catch {
    /* Analytics cannot affect the product. */
  }
}
export function capture(
  event: TelemetryEvent | "$pageview",
  path: string,
  properties: TelemetryProperties = {},
) {
  const page = safePagePath(path);
  if (!permitted || !client || !page) return;
  try {
    let referrer = "";
    try {
      referrer = document.referrer ? new URL(document.referrer).hostname : "";
    } catch {
      /* No referring host. */
    }
    const campaign: Record<string, string> = {};
    const query = new URLSearchParams(location.search);
    for (const [key, values] of Object.entries(boundedProperties)) {
      if (!key.startsWith("utm_")) continue;
      const value = query.get(key);
      if (value && values.includes(value)) campaign[key] = value;
    }
    landingPage ??= page;
    client.capture(event, {
      ...properties,
      ...campaign,
      page_path: page,
      landing_page: landingPage,
      page_category:
        page === "/builder"
          ? "builder"
          : page.startsWith("/docs/")
            ? "docs"
            : "marketing",
      referrer_host: [new URL(site.origin).hostname, "gethookway.com"].includes(
        referrer,
      )
        ? ""
        : referrer,
    });
  } catch {
    /* Navigation and editing always work without analytics. */
  }
}
