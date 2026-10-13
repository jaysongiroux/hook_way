import { Center, Loader } from "@mantine/core";
import { Suspense, lazy, useEffect } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { site } from "virtual:hookway-docs";
import { Compare } from "./marketing/Compare";
import { Marketing } from "./marketing/Marketing";
import { UseCases } from "./marketing/UseCases";

// The builder pulls React Flow, Dagre, and Dexie, so it loads on demand: a
// visitor to the overview never downloads the editor bundle.
const Documentation = lazy(() => import("./marketing/docs/Documentation"));
const Builder = lazy(() => import("./App"));

export function AppRoutes() {
  const { pathname } = useLocation();
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
              <Center mih="70vh">
                <Loader />
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
