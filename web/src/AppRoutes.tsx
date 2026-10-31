import { Center, Loader } from "@mantine/core";
import { Suspense, lazy, useEffect, useLayoutEffect, useRef } from "react";
import { Route, Routes, useLocation } from "react-router-dom";
import { DARK } from "./tokens";
import { Compare } from "./marketing/Compare";
import { Marketing } from "./marketing/Marketing";
import { UseCases } from "./marketing/UseCases";
import { AboutPage, MCPPage, NotFoundPage, PrivacyPage } from "./website/Pages";
import { applyMetadata, pageMetadata } from "./website/metadata";
import { useTelemetry } from "./shared/telemetry";
const Documentation = lazy(() => import("./marketing/docs/Documentation"));
const Builder = lazy(() => import("./App"));
function HostedBuilder() {
  const track = useTelemetry();
  const reported = useRef(false);
  useEffect(() => {
    if (!reported.current) {
      reported.current = true;
      track("builder_opened");
    }
  }, [track]);
  return <Builder />;
}
export function AppRoutes() {
  const { pathname, hash } = useLocation();
  useLayoutEffect(() => {
    if (pathname.startsWith("/docs/")) return;
    if (hash) {
      try {
        document
          .getElementById(decodeURIComponent(hash.slice(1)))
          ?.scrollIntoView({ behavior: "instant" });
      } catch {
        /* Invalid fragment. */
      }
    } else window.scrollTo({ top: 0, left: 0, behavior: "instant" });
    applyMetadata(pageMetadata(pathname));
  }, [pathname, hash]);
  return (
    <Routes>
      <Route path="/" element={<Marketing />} />
      <Route path="/use-cases" element={<UseCases />} />
      <Route path="/compare" element={<Compare />} />
      <Route path="/mcp-gateway" element={<MCPPage />} />
      <Route path="/about" element={<AboutPage />} />
      <Route path="/privacy" element={<PrivacyPage />} />
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
            <HostedBuilder />
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
      <Route path="*" element={<NotFoundPage />} />
    </Routes>
  );
}
