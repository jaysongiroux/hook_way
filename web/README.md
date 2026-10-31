# Hookway website and console

Run frontend commands from this directory with the Node version required by `package.json`.

- `npm run build`: build the website, prerender marketing routes, generate documentation/discovery files and verify published links and metadata.
- `npm run build:console`: build the self-hosted console. The build fails if marketing, website analytics or PostHog modules cross this boundary.
- From the repository root, `make website-e2e` builds the WASM validator and a synthetic analytics-enabled website, then runs desktop/mobile browser checks. All analytics requests in these tests are intercepted. Normal builds default to analytics disabled.
- `npm run test:docs` verifies the static documentation pipeline. New priority documentation must be tracked by Git and listed in `site.json.requiredDocs`.

## Rendering and discovery

`site.json` owns marketing metadata and curated AI discovery links. Published documentation metadata comes from the existing catalog. Vite's production manifest resolves imported images during SSR so initial HTML and browser hydration use the same assets.

Public content URLs use `https://www.gethookway.com` and trailing slashes. `/builder` stays unchanged and is noindex. `VERCEL_ENV=preview` or `SITE_INDEXABLE=false` produces noindex metadata. Error pages are noindex; hosting must return HTTP 404 for unknown URLs.

The sitemap includes published content only. Do not generate lastmod dates from deployment time. Revision dates in content should change when its substance changes. `llms.txt` is a concise set of links, not a claim of ranking or AI citation benefits.

## Optional website analytics

Configure `VITE_POSTHOG_PROJECT_TOKEN`, `VITE_POSTHOG_HOST` and `VITE_POSTHOG_ENABLED=true` in Vercel's production environment only. Keep local and preview defaults disabled. The project token is public browser configuration; private PostHog API credentials must never enter Vite variables or source.

The browser SDK loads only after Allow. The website remembers Allow/Decline and exposes preferences in its footer. Events before permission are not replayed. An already-permitted action may wait asynchronously for SDK initialization; actions and navigation never wait for analytics delivery.

The shared builder imports only `src/shared/telemetry.tsx`, an SDK-free interface with a no-op default. Website code supplies the implementation. Do not import the website provider, its environment settings or the SDK into console code.

Only explicit pageviews, builder mounts, successful quick-start copies, successful export dispatches and GitHub link activations are tracked. Properties are allowlisted and URLs are canonical public paths. Campaign values are bounded; unknown routes and arbitrary campaign values are discarded. Configurations, payloads, specifications, credentials, custom filenames and form contents are excluded. GeoIP enrichment and person profiles are disabled.

The SDK retains previously accepted batch/retry work after ordinary opt-out. To meet immediate withdrawal requirements, the public `before_send` hook passes sanitized events through a small cancellable, unqueued Capture API adapter and returns null to prevent SDK dispatch. Decline aborts pending fetches; delivery has no retries or historical queue. Already transmitted requests cannot be recalled. See [PostHog's explanation](https://github.com/PostHog/posthog-js/issues/5020) and the [Capture API](https://posthog.com/docs/api/capture).

Browser tests emulate a human visitor for analytics cases because PostHog deliberately filters `navigator.webdriver` and headless client hints. Production bot filtering remains enabled. The tests also prove zero requests while undecided/declined, withdrawal, deduplicated pageviews and bounded action payloads.

Exports indicate clipboard success or browser download dispatch, not a saved file, deployment or active installation. Dashboard metrics cover consented activity only. See the website privacy page for the provider and retention policy.
