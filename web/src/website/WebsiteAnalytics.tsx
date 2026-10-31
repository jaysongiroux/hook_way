import { Button, Group, Paper, Text } from "@mantine/core";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
  type PropsWithChildren,
} from "react";
import { Link, useLocation } from "react-router-dom";
import {
  TelemetryContext,
  type TelemetryEvent,
  type TelemetryProperties,
} from "../shared/telemetry";
import {
  allowAnalytics,
  analyticsEnabled,
  capture,
  declineAnalytics,
  readConsent,
  safePagePath,
  writeConsent,
  type Consent,
  CONSENT_KEY,
} from "./analytics";

const Preferences = createContext({ openPreferences: () => {} });
export const useAnalyticsPreferences = () => useContext(Preferences);
export function WebsiteAnalytics({ children }: PropsWithChildren) {
  const { pathname } = useLocation();
  const [consent, setConsent] = useState<Consent>(null);
  const [ready, setReady] = useState(false);
  const [opened, setOpened] = useState(false);
  const lastPage = useRef("");
  const decision = useRef<Consent>(null);
  useEffect(() => {
    const choice = readConsent();
    decision.current = choice;
    setConsent(choice);
    setOpened(choice === null && analyticsEnabled);
    const sync = (event: StorageEvent) => {
      if (event.key === CONSENT_KEY) {
        decision.current = readConsent();
        setConsent(decision.current);
      }
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  useEffect(() => {
    let current = true;
    setReady(false);
    lastPage.current = "";
    if (consent === "allow")
      void allowAnalytics().then((client) => {
        if (current && client) setReady(true);
      });
    else declineAnalytics();
    return () => {
      current = false;
    };
  }, [consent]);
  useEffect(() => {
    const path = safePagePath(pathname);
    if (ready && path && lastPage.current !== path) {
      lastPage.current = path;
      capture("$pageview", path);
    }
  }, [ready, pathname]);
  const track = useCallback(
    (event: TelemetryEvent, properties?: TelemetryProperties) => {
      // Only actions that occur after Allow may wait for SDK initialization.
      // Opening the builder while undecided is never replayed after permission.
      if (!analyticsEnabled || (decision.current ?? readConsent()) !== "allow")
        return;
      void allowAnalytics().then((client) => {
        if (client) capture(event, pathname, properties);
      });
    },
    [pathname],
  );
  const choose = (choice: Exclude<Consent, null>) => {
    decision.current = choice;
    writeConsent(choice);
    if (choice === "decline") declineAnalytics();
    setConsent(choice);
    setOpened(false);
  };
  const trackLink = (event: MouseEvent<HTMLDivElement>) => {
    const anchor = (event.target as Element).closest?.("a[href]");
    try {
      if (
        anchor &&
        new URL((anchor as HTMLAnchorElement).href).hostname === "github.com"
      )
        track("github_opened");
    } catch {
      /* Invalid external link. */
    }
  };
  return (
    <Preferences.Provider value={{ openPreferences: () => setOpened(true) }}>
      <TelemetryContext.Provider value={track}>
        <div onClickCapture={trackLink}>{children}</div>
        {opened && (
          <Paper
            component="aside"
            aria-label="Optional website analytics"
            withBorder
            p="md"
            radius="sm"
            style={{
              position: "fixed",
              bottom: 16,
              right: 16,
              width: "min(420px, calc(100vw - 32px))",
              zIndex: 1000,
            }}
          >
            <Text fw={600}>Optional website analytics</Text>
            <Text size="sm" mt={4}>
              {analyticsEnabled
                ? "Allow page and action counts to help improve Hookway? Editor contents are excluded. No session recording."
                : "Optional analytics are currently disabled. You can still save your preference."}{" "}
              <Link to="/privacy/">Privacy details</Link>
            </Text>
            <Group mt="sm" gap="sm">
              <Button
                size="xs"
                variant="default"
                onClick={() => choose("decline")}
              >
                Decline
              </Button>
              <Button size="xs" onClick={() => choose("allow")}>
                Allow
              </Button>
            </Group>
          </Paper>
        )}
      </TelemetryContext.Provider>
    </Preferences.Provider>
  );
}
