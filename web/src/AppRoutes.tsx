import { Center, Loader } from "@mantine/core";
import { Suspense, lazy, useEffect, useLayoutEffect } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { site } from "virtual:hookway-docs";
import { DARK } from "./tokens";
import { Compare } from "./marketing/Compare";
import { Marketing } from "./marketing/Marketing";
import { UseCases } from "./marketing/UseCases";

// The builder pulls React Flow, Dagre, and Dexie, so it loads on demand: a
// visitor to the overview never downloads the editor bundle.
const Documentation = lazy(() => import("./marketing/docs/Documentation"));
const Builder = lazy(() => import("./App"));

export function AppRoutes() {
  const { pathname, hash } = useLocation();
  useLayoutEffect(() => {
    if (!["/", "/use-cases", "/compare"].includes(pathname)) return;
    // Reset before paint so a newly mounted page never flashes at the old offset.
    // Documentation owns its scroll position once its async content is ready.
    if (hash) {
      let id = hash.slice(1);
      try {
        id = decodeURIComponent(id);
      } catch {
        // A malformed fragment simply has no matching section.
      }
      document.getElementById(id)?.scrollIntoView({ behavior: "instant" });
    } else {
      window.scrollTo({ top: 0, left: 0, behavior: "instant" });
    }
  }, [pathname, hash]);
  useEffect(() => {
    if (/^\/docs(?:\/|$)/.test(pathname)) return;
    // Docs and marketing share a document: each route must claim its metadata.
    let description = document.querySelector<HTMLMetaElement>(
      'meta[name="description"]',
    );
    if (!description) {
      description = document.createElement("meta");
      description.name = "description";
      document.head.append(description);
    }
    description.content = site.description;
    let canonical = document.querySelector<HTMLLinkElement>(
      'link[rel="canonical"]',
    );
    if (!canonical) {
      canonical = document.createElement("link");
      canonical.rel = "canonical";
      document.head.append(canonical);
    }
    canonical.href = site.origin + pathname;
  }, [pathname]);
  return (
    <Routes>
      <Route path="/" element={<Marketing />} />
      <Route path="/use-cases" element={<UseCases />} />
      <Route path="/compare" element={<Compare />} />
      <Route
        path="/builder"
        element={
          <Suspense
            fallback={
              <Center h="100vh">
                <Loader />
              </Center>
            }
          >
            <Builder />
          </Suspense>
        }
      />
      <Route
        path="/docs/*"
        element={
          <Suspense
            fallback={
              <Center
                mih="100dvh"
                style={{ background: DARK.bg }}
                data-docs-loading
              >
                <Loader aria-label="Loading documentation" />
              </Center>
            }
          >
            <Documentation />
          </Suspense>
        }
      />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
