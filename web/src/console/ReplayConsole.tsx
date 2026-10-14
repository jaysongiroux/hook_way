import React from "react";
import { Link, useLocation } from "react-router-dom";
import { DARK } from "../shared/ui/tokens";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Group,
  PasswordInput,
  Select,
  Menu,
  Skeleton,
  Tabs,
  SimpleGrid,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from "@mantine/core";
import {
  FiChevronDown,
  FiChevronLeft,
  FiFilter,
  FiInbox,
  FiRefreshCw,
} from "react-icons/fi";
import { RecordState } from "./RecordState";
import { Logo } from "../shared/ui/Logo";
import { HEADER_STYLE } from "../shared/ui/chrome";
import { EmptyState, ErrorState, LoadingState } from "../shared/ui/States";
import { loadWasm, retryWasm } from "../shared/wasm/loader";
import type { paths } from "../shared/contracts/generated/replay";
import {
  createSession,
  APIError,
  requestError,
  result,
  timestamp,
  type RecordDetailData,
  type ReplayRecord,
  type Identity,
  type Session,
} from "./session";
import { ActionDialog, type Intent } from "./ActionDialog";
import { RecordDetail } from "./RecordDetail";
import "./console.css";

const Builder = React.lazy(() => import("../App"));

type Filters = {
  state: string;
  flow: string;
  node_id: string;
  destination_id: string;
  retryable: string;
  since: string;
  until: string;
};
const initialFilters: Filters = {
  state: "open",
  flow: "",
  node_id: "",
  destination_id: "",
  retryable: "",
  since: "",
  until: "",
};
type Query = NonNullable<paths["/v1/records"]["get"]["parameters"]["query"]>;
const SESSION_IDLE_TIMEOUT_MS = 10 * 60 * 1000;
type StoredSession = { credential: string; lastActivity: number };

function readStoredSession(key: string): StoredSession | undefined {
  try {
    const stored = localStorage.getItem(key);
    if (!stored) return;
    const value = JSON.parse(stored) as Partial<StoredSession>;
    if (
      typeof value.credential !== "string" ||
      !value.credential ||
      typeof value.lastActivity !== "number" ||
      !Number.isFinite(value.lastActivity)
    ) {
      localStorage.removeItem(key);
      return;
    }
    if (Date.now() - value.lastActivity >= SESSION_IDLE_TIMEOUT_MS) {
      localStorage.removeItem(key);
      return;
    }
    return value as StoredSession;
  } catch {
    return;
  }
}

function writeStoredSession(key: string, credential: string, lastActivity = Date.now()) {
  try {
    localStorage.setItem(key, JSON.stringify({ credential, lastActivity }));
  } catch {
    // Keep the active session usable when browser storage is unavailable.
  }
}

function clearStoredSession(key: string) {
  try {
    localStorage.removeItem(key);
  } catch {
    // Storage may be disabled by the browser.
  }
}

function queryOf(filters: Filters, cursor: string, inspection: boolean): Query {
  const query: Query = { limit: 50 };
  query.kind = inspection ? "inspection" : "failure";
  if (cursor) query.cursor = cursor;
  if (filters.state === "dismissed") query.dismissed = true;
  else if (filters.state) {
    query.dismissed = false;
    if (!inspection) query.delivery_status = filters.state as Query["delivery_status"];
  }
  for (const key of ["flow", "node_id", "destination_id"] as const)
    if (filters[key]) query[key] = filters[key];
  if (filters.retryable) query.retryable = filters.retryable === "true";
  for (const key of ["since", "until"] as const)
    if (filters[key]) query[key] = new Date(filters[key]).toISOString();
  return query;
}

export function ReplayConsole({ apiUrl }: { apiUrl: string }) {
  const builder = useLocation().pathname === "/builder";
  const storageKey = `hookway.replay.session:${apiUrl}`;
  React.useEffect(() => { document.title = builder ? "Hookway builder" : "Hookway replay console"; }, [builder]);
  const [wasm, setWasm] = React.useState<"loading" | "ready" | "error">(
    "loading",
  );
  const [credential, setCredential] = React.useState("");
  const [identity, setIdentity] = React.useState<Identity>();
  const [session, setSession] = React.useState<Session>();
  const activeSession = React.useRef<Session | undefined>(undefined);
  const [loginBusy, setLoginBusy] = React.useState(false);
  const [loginError, setLoginError] = React.useState("");
  const idleTimer = React.useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const logout = React.useCallback(() => {
    if (idleTimer.current) clearTimeout(idleTimer.current);
    clearStoredSession(storageKey);
    activeSession.current?.dispose();
    activeSession.current = undefined;
    setSession(undefined);
    setIdentity(undefined);
    setCredential("");
    setLoginError("");
  }, [storageKey]);
  const initialize = React.useCallback(async (retry = false) => {
    setWasm("loading");
    try {
      const options = { assetBase: import.meta.env.BASE_URL };
      await (retry ? retryWasm(options) : loadWasm(options));
      setWasm("ready");
    } catch {
      setWasm("error");
    }
  }, []);
  React.useEffect(() => {
    void initialize();
  }, [initialize]);
  async function authenticate(token: string, restore = false) {
    setLoginBusy(true);
    setLoginError("");
    const candidate = createSession(token, () => {
      logout();
      setLoginError(
        "Your API key was rejected. Sign in again with a valid operator key.",
      );
    }, apiUrl);
    activeSession.current = candidate;
    try {
      const me = result(
        await candidate.client.GET("/v1/me", { signal: candidate.signal }),
      );
      if (me.contract_version !== 1)
        throw new Error("This console requires replay API contract version 1. Update the replay service and console together.");
      if (
        ![
          "record_api",
          "failure_inspection",
          "destination_retry",
          "batch_retry",
          "unknown_resolution",
          "action_input_validation",
          "action_eligibility",
        ].every((capability) => me.capabilities.includes(capability)) ||
        !me.scopes.includes("read")
      )
        throw new Error(
          "The replay API contract is incompatible or this key has no read scope. Update matching console/API assets and use an operator key.",
        );
      if (!candidate.signal.aborted) {
        setIdentity(me);
        setSession(candidate);
        setCredential("");
        writeStoredSession(storageKey, token);
      }
    } catch (caught) {
      const cancelled =
        candidate.signal.aborted && !(caught instanceof APIError);
      candidate.dispose();
      if (activeSession.current === candidate)
        activeSession.current = undefined;
      if (!cancelled) {
        clearStoredSession(storageKey);
        if (restore) setCredential("");
        setLoginError(requestError(caught, "Sign-in failed."));
      }
    } finally {
      setLoginBusy(false);
    }
  }
  React.useEffect(() => {
    const stored = readStoredSession(storageKey);
    if (stored) void authenticate(stored.credential, true);
    return () => {
      activeSession.current?.dispose();
      activeSession.current = undefined;
    };
    // Each mount owns its session, including StrictMode's setup/cleanup cycle.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storageKey]);
  React.useEffect(() => {
    if (!session) return;
    const stored = readStoredSession(storageKey);
    if (!stored) {
      logout();
      return;
    }
    let lastWrite = 0;
    const scheduleExpiry = (lastActivity: number) => {
      if (idleTimer.current) clearTimeout(idleTimer.current);
      const remaining = SESSION_IDLE_TIMEOUT_MS - (Date.now() - lastActivity);
      if (remaining <= 0) logout();
      else idleTimer.current = setTimeout(logout, remaining);
    };
    const activity = () => {
      const now = Date.now();
      stored.lastActivity = now;
      scheduleExpiry(now);
      if (now - lastWrite >= 10_000) {
        writeStoredSession(storageKey, stored.credential, now);
        lastWrite = now;
      }
    };
    const onStorage = (event: StorageEvent) => {
      if (event.key !== storageKey) return;
      const latest = readStoredSession(storageKey);
      if (!latest) logout();
      else {
        stored.lastActivity = latest.lastActivity;
        scheduleExpiry(latest.lastActivity);
      }
    };
    scheduleExpiry(stored.lastActivity);
    const activityEvents = ["pointerdown", "keydown", "touchstart", "mousemove", "scroll"] as const;
    for (const event of activityEvents)
      window.addEventListener(event, activity, { passive: true });
    window.addEventListener("storage", onStorage);
    return () => {
      if (idleTimer.current) clearTimeout(idleTimer.current);
      for (const event of activityEvents) window.removeEventListener(event, activity);
      window.removeEventListener("storage", onStorage);
    };
  }, [session, storageKey, logout]);
  async function login(e: React.FormEvent) {
    e.preventDefault();
    if (loginBusy) return;
    const token = credential.trim();
    if (!token) return;
    await authenticate(token);
  }
  const accountMenu = identity && (
    <Menu position="bottom-end" width={270}>
      <Menu.Target>
        <Button
          variant="subtle"
          size="sm"
          title={identity.key_name}
          rightSection={<FiChevronDown aria-hidden="true" />}
        >
          <Text component="span" size="sm" className="operator-name" truncate>
            {identity.key_name}
          </Text>
        </Button>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Label>Current API key</Menu.Label>
        <Box px="sm" pb="sm">
          <Text size="sm" fw={600}>{identity.key_name}</Text>
          <Text size="xs" c="dimmed">Scopes: {identity.scopes.join(", ")}</Text>
          <Text size="xs" c="dimmed" mt={6}>
            Session expires after 10 minutes without activity.
          </Text>
        </Box>
        <Menu.Divider />
        <Menu.Item onClick={logout}>Sign out</Menu.Item>
      </Menu.Dropdown>
    </Menu>
  );
  return (
    <Box
      className={`replay-app ${session || builder ? "is-authenticated" : ""}`}
      style={
        {
          "--console-bg": DARK.bg,
          "--console-surface": DARK.surface,
          "--console-text": DARK.text,
          "--console-border": DARK.border,
          "--console-muted": DARK.muted,
          "--console-accent": DARK.accent,
        } as React.CSSProperties
      }
    >
      {!builder && <header className="console-header" style={HEADER_STYLE}>
        <Group gap="md" wrap="nowrap">
          <Logo to="/" assetBase={import.meta.env.BASE_URL} />
          <span className="header-separator" aria-hidden="true" />
          <Title order={1} size="h4">
            Replay console
          </Title>
        </Group>
        <Group gap="xs" wrap="nowrap">
          <Button component={Link} to="/builder" variant="default" size="xs">
            Builder
          </Button>
          {accountMenu}
        </Group>
      </header>}
      <main className="console-main">
        {!builder && wasm === "loading" && (
          <div className="validation-notice">
            <LoadingState>Loading local validation…</LoadingState>
          </div>
        )}
        {!builder && wasm === "error" && (
          <div className="validation-notice">
            <ErrorState
              title="Local validation is unavailable"
              onRetry={() => void initialize(true)}
            >
              Operations remain available through the API input validation path.
              Restore matching WASM assets to validate locally.
            </ErrorState>
          </div>
        )}
        {session && identity ? (
          <Records
            key={identity.key_name}
            session={session}
            identity={identity}
            active={!builder}
          />
        ) : !builder ? (
          <div className="console-signin">
            <section
              className="signin-context"
              aria-label="About the replay console"
            >
              <Title order={2}>
                Investigate a delivery.
                <br />
                Choose the next step.
              </Title>
              <Text c="dimmed">
                Inspect what failed, check the delivery evidence, and recover
                the captured destination with a deliberate retry.
              </Text>
              <ol className="signin-workflow">
                <li>
                  <strong>Find the failure</strong>
                  <span>Narrow the inbox by state, flow or destination.</span>
                </li>
                <li>
                  <strong>Check the evidence</strong>
                  <span>Read errors, retry history and captured payloads.</span>
                </li>
                <li>
                  <strong>Recover deliberately</strong>
                  <span>
                    Retry, confirm an uncertain outcome or stop recovery.
                  </span>
                </li>
              </ol>
            </section>
            <Box
              component="form"
              className="signin-form"
              onSubmit={(e) => void login(e)}
            >
              <Stack gap="md">
                <div>
                  <Title order={2} size="h3">
                    Sign in to inspect deliveries
                  </Title>
                  <Text size="sm" c="dimmed" mt={6}>
                    Use a named token from your replay service.
                  </Text>
                </div>
                <PasswordInput
                  label="Replay API token"
                  description="Read scope is required. Retry and payload scopes enable additional actions."
                  autoComplete="off"
                  value={credential}
                  onChange={(e) => setCredential(e.currentTarget.value)}
                  required
                  disabled={loginBusy}
                />
                {loginError && (
                  <Alert color="red" title="Sign-in failed">
                    {loginError}
                  </Alert>
                )}
                <Button
                  type="submit"
                  loading={loginBusy}
                  disabled={!credential.trim()}
                >
                  Sign in
                </Button>
                <Text size="xs" c="dimmed">
                  Your token is saved in this browser for up to 10 minutes of
                  inactivity. Sign out to clear it now.
                </Text>
              </Stack>
            </Box>
          </div>
        ) : null}
        {builder && <React.Suspense fallback={<LoadingState>Loading builder…</LoadingState>}>
          <Builder product headerActions={<>
            <Button component={Link} to="/" variant="default" size="xs" leftSection={<FiChevronLeft aria-hidden="true" />}>
              Replay console
            </Button>
            {accountMenu}
          </>} />
        </React.Suspense>}
      </main>
    </Box>
  );
}

function Records({ session, identity, active }: { session: Session; identity: Identity; active: boolean }) {
  const [kind, setKind] = React.useState("failures");
  const hasInspections = identity.capabilities.includes("inspection_records");
  return (
    <>
      {active && hasInspections && (
        <Tabs id="record-kind" value={kind} onChange={(value) => setKind(value || "failures")} className="record-navigation">
          <Tabs.List aria-label="Record kind">
            <Tabs.Tab value="failures" aria-controls="record-kind-panel-failures">Failures</Tabs.Tab>
            <Tabs.Tab value="inspections" aria-controls="record-kind-panel-inspections">Inspections</Tabs.Tab>
          </Tabs.List>
        </Tabs>
      )}
      <Inbox session={session} identity={identity} active={active && kind === "failures"} inspection={false} />
      {hasInspections && <Inbox session={session} identity={identity} active={active && kind === "inspections"} inspection />}
    </>
  );
}

function Inbox({
  session,
  identity,
  active,
  inspection,
}: {
  session: Session;
  identity: Identity;
  active: boolean;
  inspection: boolean;
}) {
  const [draft, setDraft] = React.useState(() => ({ ...initialFilters, state: inspection ? "available" : "open" }));
  const [filtersOpen, setFiltersOpen] = React.useState(false);
  const inboxRef = React.useRef<HTMLElement>(null);
  const listRef = React.useRef<HTMLDivElement>(null);
  const inboxPosition = React.useRef(0);
  const inspectRef = React.useRef<HTMLButtonElement>(null);
  const [filters, setFilters] = React.useState(() => ({ ...initialFilters, state: inspection ? "available" : "open" }));
  const [cursors, setCursors] = React.useState<string[]>([""]);
  const cursor = cursors[cursors.length - 1];
  React.useEffect(() => {
    if (listRef.current) listRef.current.scrollTop = 0;
  }, [filters, cursor]);
  const [next, setNext] = React.useState("");
  const [items, setItems] = React.useState<ReplayRecord[]>([]);
  const [selected, setSelected] = React.useState("");
  const [detail, setDetail] = React.useState<RecordDetailData>();
  const [checked, setChecked] = React.useState<string[]>([]);
  const [error, setError] = React.useState("");
  const [detailError, setDetailError] = React.useState("");
  const [filterError, setFilterError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [updated, setUpdated] = React.useState("");
  const [refresh, setRefresh] = React.useState(0);
  const [intent, setIntent] = React.useState<Intent>();
  const [dialogOpen, setDialogOpen] = React.useState(false);
  const panelRef = React.useRef<HTMLElement>(null);
  const actionTriggerRef = React.useRef<HTMLElement | null>(null);
  const focusPending = React.useRef("");
  React.useEffect(() => {
    if (detail?.id && detail.id === focusPending.current) {
      focusPending.current = "";
      if (window.matchMedia("(max-width: 1000px)").matches) {
        panelRef.current?.scrollIntoView({ block: "start" });
        panelRef.current?.focus({ preventScroll: true });
      }
    }
  }, [detail?.id]);
  const select = (id: string) => {
    if (!selected || !window.matchMedia("(max-width: 1000px)").matches)
      inboxPosition.current = listRef.current?.scrollTop || 0;
    focusPending.current = id;
    setSelected(id);
    setDetail(undefined);
    setDetailError("");
  };
  const refreshNow = () => setRefresh((value) => value + 1);
  const action = (value: Intent) => {
    if (intent) return;
    actionTriggerRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    setIntent(value);
    setDialogOpen(true);
  };
  React.useEffect(() => {
    if (!active) return;
    let live = true;
    let running = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    const abort = () => controller?.abort();
    session.signal.addEventListener("abort", abort);
    async function poll() {
      if (
        !live ||
        document.visibilityState !== "visible" ||
        session.signal.aborted ||
        running
      )
        return;
      running = true;
      controller = new AbortController();
      setBusy(true);
      try {
        const page = result(
          await session.client.GET("/v1/records", {
            params: { query: queryOf(filters, cursor, inspection) },
            signal: controller.signal,
          }),
        );
        if (live && !controller.signal.aborted) {
          setItems(page.items);
          setNext(page.next_cursor || "");
          setError("");
          setUpdated(new Date().toLocaleTimeString());
        }
        if (selected) {
          try {
            const record = result(
              await session.client.GET("/v1/records/{id}", {
                params: { path: { id: selected } },
                signal: controller.signal,
              }),
            );
            if (live && !controller.signal.aborted) {
              setDetail(record);
              setDetailError("");
            }
          } catch (caught) {
            if (live && !controller.signal.aborted) {
              setDetail(undefined);
              setDetailError(
                requestError(caught, "Could not load this record."),
              );
            }
          }
        }
      } catch (caught) {
        if (live && !controller.signal.aborted)
          setError(requestError(caught, inspection ? "Could not load inspections." : "Could not load failures."));
      } finally {
        running = false;
        if (live) {
          setBusy(false);
          if (document.visibilityState === "visible")
            timer = setTimeout(() => void poll(), 3000);
        }
      }
    }
    function visibility() {
      if (timer) clearTimeout(timer);
      if (document.visibilityState === "visible") void poll();
      else abort();
    }
    document.addEventListener("visibilitychange", visibility);
    void poll();
    return () => {
      live = false;
      if (timer) clearTimeout(timer);
      abort();
      session.signal.removeEventListener("abort", abort);
      document.removeEventListener("visibilitychange", visibility);
    };
  }, [session, filters, cursor, selected, refresh, active, inspection]);
  const apply = (e: React.FormEvent) => {
    e.preventDefault();
    setFilterError("");
    if (
      draft.since &&
      draft.until &&
      new Date(draft.since) >= new Date(draft.until)
    ) {
      setFilterError("The lower time bound must precede the upper bound.");
      return;
    }
    setFilters({ ...draft });
    setCursors([""]);
    setChecked([]);
    setItems([]);
    setNext("");
  };
  function move(cursors: string[]) {
    setCursors(cursors);
    setItems([]);
    setChecked([]);
    setNext("");
  }
  function closeDetail() {
    const trigger = inspectRef.current;
    setSelected("");
    setDetail(undefined);
    requestAnimationFrame(() => {
      if (listRef.current) listRef.current.scrollTop = inboxPosition.current;
      if (trigger?.isConnected) trigger.focus({ preventScroll: true });
      else inboxRef.current?.focus({ preventScroll: true });
    });
  }
  function changeState(value: string | null) {
    const state = value === "all" ? "" : value || "";
    setDraft((current) => ({ ...current, state }));
    setFilters((current) => ({ ...current, state }));
    move([""]);
  }
  const unapplied = JSON.stringify(draft) !== JSON.stringify(filters);
  const advancedCount = [
    filters.node_id,
    filters.destination_id,
    filters.retryable,
    filters.since,
    filters.until,
  ].filter(Boolean).length;
  const selectedRecords = items.filter((item) => checked.includes(item.id));
  return (
    <>
    {active && <div className={`console-investigation ${selected ? "has-detail" : ""}`} id={`record-kind-panel-${inspection ? "inspections" : "failures"}`} role={identity.capabilities.includes("inspection_records") ? "tabpanel" : undefined} aria-labelledby={identity.capabilities.includes("inspection_records") ? `record-kind-tab-${inspection ? "inspections" : "failures"}` : undefined}>
      <div
        className="console-workspace"
        id="delivery-state-results"
        role="tabpanel"
        aria-labelledby={`delivery-state-${filters.state || "all"}`}
      >
        <section
          aria-label={inspection ? "Inspection inbox" : "Failure inbox"}
          className="failure-inbox"
          tabIndex={-1}
          ref={inboxRef}
        >
      <div className="inbox-chrome">
        <div className="console-page-heading">
          <div>
            <Title order={2} size="h3">
              {inspection ? "Webhook inspections" : "Delivery failures"}
            </Title>
            <Text size="sm" c="dimmed">
              {inspection ? "Spot-check captured webhooks. These records are view only." : "Investigate captured deliveries and recover the destination."}
            </Text>
          </div>
          <Button
            variant="default"
            size="xs"
            leftSection={<FiRefreshCw aria-hidden="true" />}
            onClick={refreshNow}
            disabled={busy}
          >
            Refresh
          </Button>
        </div>
        <Tabs
          value={filters.state || "all"}
          onChange={changeState}
          className="state-navigation"
        >
          <Tabs.List aria-label={inspection ? "Inspection state" : "Delivery state"}>
            {(inspection ? [
              { value: "available", label: "Available" },
              { value: "dismissed", label: "Dismissed" },
              { value: "all", label: "All inspections" },
            ] : [
              { value: "open", label: "Open failures" },
              { value: "unknown", label: "Unknown outcome" },
              { value: "retrying", label: "Retry in progress" },
              { value: "resolved", label: "Resolved" },
              { value: "dismissed", label: "Dismissed" },
              { value: "all", label: "All states" },
            ]).map((state) => (
              <Tabs.Tab
                key={state.value}
                value={state.value}
                id={`delivery-state-${state.value}`}
                aria-controls="delivery-state-results"
              >
                {state.label}
              </Tabs.Tab>
            ))}
          </Tabs.List>
        </Tabs>
        {filterError && (
          <Alert color="red" title="Check your filters" mb="sm">
            {filterError}
          </Alert>
        )}
        <Box component="form" onSubmit={apply} className="console-filters">
          <div className="filter-toolbar">
            <TextInput
              label="Flow"
              placeholder="All flows"
              value={draft.flow}
              onChange={(e) =>
                setDraft({ ...draft, flow: e.currentTarget.value })
              }
              size="xs"
            />
            <Button type="submit" size="xs" variant="default">
              Apply filters
            </Button>
            <Button
              size="xs"
              variant={filtersOpen || advancedCount ? "light" : "subtle"}
              leftSection={<FiFilter aria-hidden="true" />}
              onClick={() => setFiltersOpen((open) => !open)}
              aria-expanded={filtersOpen}
              aria-controls="advanced-filters"
            >
              Filters{advancedCount ? ` (${advancedCount})` : ""}
            </Button>
            {unapplied && (
              <Text size="xs" c="dimmed" className="filter-change-note">
                Unapplied changes
              </Text>
            )}
            {(filters.flow || advancedCount > 0) && (
              <Button
                size="xs"
                variant="subtle"
                onClick={() => {
                  const cleared = { ...initialFilters, state: filters.state };
                  setFilterError("");
                  setDraft(cleared);
                  setFilters(cleared);
                  move([""]);
                }}
              >
                Clear filters
              </Button>
            )}
          </div>
          <div
            id="advanced-filters"
            className="advanced-filters"
            hidden={!filtersOpen}
          >
            <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
              {(inspection ? ["node_id"] as const : ["node_id", "destination_id"] as const).map((key) => (
                <TextInput
                  key={key}
                  label={key === "node_id" ? "Node ID" : "Destination ID"}
                  size="xs"
                  value={draft[key]}
                  onChange={(e) =>
                    setDraft({ ...draft, [key]: e.currentTarget.value })
                  }
                />
              ))}
              {!inspection && <Select
                label="Captured retry capability"
                description="State and retention also determine availability."
                size="xs"
                value={draft.retryable}
                data={[
                  { value: "", label: "All records" },
                  { value: "true", label: "Retry artifacts captured" },
                  { value: "false", label: "Metadata only" },
                ]}
                onChange={(value) =>
                  setDraft({ ...draft, retryable: value || "" })
                }
                allowDeselect={false}
              />}
              <TextInput
                type="datetime-local"
                label={inspection ? "Observed since (inclusive)" : "Failed since (inclusive)"}
                description="Your local time zone."
                size="xs"
                value={draft.since}
                onChange={(e) =>
                  setDraft({ ...draft, since: e.currentTarget.value })
                }
              />
              <TextInput
                type="datetime-local"
                label={inspection ? "Observed before (exclusive)" : "Failed before (exclusive)"}
                description="Your local time zone."
                size="xs"
                value={draft.until}
                onChange={(e) =>
                  setDraft({ ...draft, until: e.currentTarget.value })
                }
              />
            </SimpleGrid>
          </div>
        </Box>
      </div>
      {error && (
        <ErrorState title="Inbox refresh failed" onRetry={refreshNow}>
          {error}{" "}
          {updated &&
            "Previously loaded data may be stale. Actions are checked again by the API."}
        </ErrorState>
      )}
      {intent && !dialogOpen && (
        <Alert
          color="yellow"
          title="A submission needs confirmation"
          className="pending-action"
        >
          <Group justify="space-between">
            <Text size="sm">
              Inspect the history before deciding what to do next.
            </Text>
            <Button
              size="xs"
              color="yellow"
              onClick={() => setDialogOpen(true)}
            >
              Continue uncertain request
            </Button>
          </Group>
        </Alert>
      )}
          <div className="inbox-panel-heading">
            <Title order={3} size="h5">
              {inspection ? "Inspection inbox" : "Failure inbox"}
            </Title>
            <Text size="xs" c="dimmed">
              {items.length} on this page
            </Text>
          </div>
          {!inspection && identity.scopes.includes("retry") && checked.length > 0 && (
            <div className="batch-toolbar">
              <Text size="sm">{selectedRecords.length} selected</Text>
              <Group gap="xs">
                <Button
                  size="xs"
                  variant="subtle"
                  onClick={() => setChecked([])}
                >
                  Clear selection
                </Button>
                <Button
                  size="xs"
                  disabled={!selectedRecords.length || !!intent}
                  onClick={() =>
                    action({ action: "retry", records: selectedRecords })
                  }
                >
                  Retry selected ({selectedRecords.length})
                </Button>
              </Group>
            </div>
          )}
          <div className="inbox-scroll" ref={listRef}>
            {!items.length ? (
              busy ? (
                <Stack p="md" aria-label={inspection ? "Loading inspection inbox" : "Loading failure inbox"}>
                  {[1, 2, 3, 4].map((i) => (
                    <Skeleton key={i} height={74} radius="sm" />
                  ))}
                </Stack>
              ) : (
                <div className="inbox-empty-state">
                  <EmptyState>
                    {error
                      ? "The inbox could not be loaded."
                      : inspection ? "No inspections match these filters. Connect an Inspect node after Envelope, or after Sample on an observation branch." : "No failures match these filters. Try another state or clear your filters."}
                  </EmptyState>
                </div>
              )
            ) : (
              <Table highlightOnHover className="failure-table">
                <Table.Thead>
                  <Table.Tr>
                    {!inspection && identity.scopes.includes("retry") && (
                      <Table.Th className="selection-heading">
                        <span className="visually-hidden">Select</span>
                      </Table.Th>
                    )}
                    <Table.Th>{inspection ? "Webhook" : "Delivery"}</Table.Th>
                    <Table.Th>{inspection ? "Observation" : "Outcome"}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {items.map((item) => (
                    <Table.Tr
                      key={item.id}
                      data-selected={item.id === selected || undefined}
                    >
                      {!inspection && identity.scopes.includes("retry") && (
                        <Table.Td className="failure-selection">
                          <Checkbox
                            size="xs"
                            aria-label={`Select ${item.id}`}
                            checked={checked.includes(item.id)}
                            disabled={!item.actions.retry.allowed || !!intent}
                            onChange={(e) =>
                              setChecked(
                                e.currentTarget.checked
                                  ? [...checked, item.id]
                                  : checked.filter((id) => id !== item.id),
                              )
                            }
                          />
                        </Table.Td>
                      )}
                      <Table.Td className="failure-identity">
                        <Button
                          ref={item.id === selected ? inspectRef : undefined}
                          variant="subtle"
                          className="failure-link"
                          onClick={() => select(item.id)}
                          aria-label={`Inspect ${item.id}`}
                        >
                          <span className="failure-row-title">
                            {item.flow} <span className="row-slash">/</span>{" "}
                            {item.node_id}
                          </span>
                          <span className="failure-row-destination">
                            {item.destination_id || item.node_type}
                          </span>
                          <span className="identifier row-id">{item.id}</span>
                        </Button>
                        {!inspection && !item.actions.retry.allowed && (
                          <Text size="xs" c="dimmed" className="row-reason">
                            {item.actions.retry.reason}
                          </Text>
                        )}
                      </Table.Td>
                      <Table.Td className="failure-state">
                        <RecordState record={item} />
                        <time
                          dateTime={item.occurred_at}
                          title={timestamp(item.occurred_at)}
                        >
                          {new Date(item.occurred_at).toLocaleString(undefined, {
                            month: "short",
                            day: "numeric",
                            hour: "numeric",
                            minute: "2-digit",
                          })}
                        </time>
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            )}
          </div>
          <div className="inbox-footer">
            <Group gap="xs">
              <Button
                size="xs"
                variant="default"
                disabled={cursors.length === 1 || busy}
                onClick={() => move(cursors.slice(0, -1))}
              >
                Previous page
              </Button>
              <Text size="xs">Page {cursors.length}</Text>
              <Button
                size="xs"
                variant="default"
                disabled={!next || busy}
                onClick={() => move([...cursors, next])}
              >
                Next page
              </Button>
            </Group>
            <Text size="xs" c="dimmed" className="refresh-note">
              {updated
                ? `Last refreshed ${updated}. Updates every 3 seconds while this page is visible.`
                : inspection ? "Loading inspection inbox…" : "Loading failure inbox…"}
            </Text>
          </div>
        </section>
        <section
          aria-label={selected ? (inspection ? "Selected inspection" : "Selected failure") : "Investigation panel"}
          ref={panelRef}
          tabIndex={-1}
          className="detail-panel"
        >
          {selected ? (
            <>
              <div className="detail-navigation">
                <Button
                  size="xs"
                  variant="subtle"
                  leftSection={<FiChevronLeft aria-hidden="true" />}
                  onClick={closeDetail}
                >
                  <span className="desktop-close-label">Close details</span>
                  <span className="mobile-close-label">Back to inbox</span>
                </Button>
                <Text size="xs" c="dimmed">
                  {inspection ? "Webhook observation" : "Delivery investigation"}
                </Text>
              </div>
              {detailError ? (
                <ErrorState title="Record unavailable" onRetry={refreshNow}>
                  {detailError}
                </ErrorState>
              ) : detail ? (
                <RecordDetail
                  key={detail.id}
                  record={detail}
                  session={session}
                  scopes={identity.scopes}
                  onSelect={select}
                  onAction={action}
                  actionPending={!!intent}
                />
              ) : (
                <Stack p="lg" aria-label={inspection ? "Loading inspection details" : "Loading failure details"}>
                  <Skeleton height={28} width="65%" />
                  <Skeleton height={70} />
                  <Skeleton height={140} />
                </Stack>
              )}
            </>
          ) : (
            <div className="investigation-empty">
              <FiInbox size={32} aria-hidden="true" />
              <Title order={3} size="h3">
                {inspection ? "Select a webhook to inspect" : "Select a delivery to investigate"}
              </Title>
              <Text size="sm" c="dimmed">
                {inspection ? "See the body and context captured at the Inspect node, alongside the final response sent to the webhook sender." : "Check its error and delivery history, then choose the next safe action. Selecting a row leaves your inbox and filters in place."}
              </Text>
              <div className="investigation-guide">
                <Text size="sm" fw={600}>
                  {inspection ? "A snapshot of one moment" : "An unknown outcome needs evidence"}
                </Text>
                <Text size="sm" c="dimmed">
                  {inspection ? "Inspections share replay retention and storage limits. They cannot be retried or confirmed as delivery outcomes." : "A lost response does not prove delivery failed. Check the destination before retrying."}
                </Text>
              </div>
            </div>
          )}
        </section>
      </div>
    </div>}
      {intent && (
        <ActionDialog
          intent={intent}
          session={session}
          opened={active && dialogOpen}
          onRefresh={refreshNow}
          onClose={(retain, completed) => {
            setDialogOpen(false);
            if (!retain) setIntent(undefined);
            requestAnimationFrame(() => {
              const trigger = actionTriggerRef.current;
              // Completed actions can remove their trigger in a later refresh.
              if (!completed && trigger?.isConnected && !trigger.matches(":disabled"))
                trigger.focus({ preventScroll: true });
              else (selected ? panelRef.current : inboxRef.current)?.focus({ preventScroll: true });
            });
          }}
        />
      )}
    </>
  );
}
