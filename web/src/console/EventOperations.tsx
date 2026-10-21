import React from "react";
import { Alert, Badge, Button, Checkbox, Group, Modal, Select, Stack, Table, Tabs, Text, Textarea, TextInput, Title } from "@mantine/core";
import { FiArrowLeft, FiRefreshCw } from "react-icons/fi";
import type { components } from "../shared/contracts/generated/replay";
import { hasWasmCapability, invokeWasm } from "../shared/wasm/loader";
import { EmptyState, LoadingState } from "../shared/ui/States";
import { APIError, result, timestamp, type Identity, type Session } from "./session";

type Subscription = components["schemas"]["MCPSubscription"];
type Delivery = components["schemas"]["MCPDelivery"];
type Kind = "subscriptions" | "deliveries";
type Detail = Subscription | Delivery;
type Link = { kind: Kind; id?: string; subscription?: string; serial: number };
type Filters = { endpoint: string; event: string; state: string; owner: string; subscription: string; since: string; until: string };
const emptyFilters: Filters = { endpoint: "", event: "", state: "", owner: "", subscription: "", since: "", until: "" };
const delivery = (value: Detail): value is Delivery => typeof (value as Delivery).subscription_id === "string";
function errorText(error: unknown) {
  if (error instanceof APIError && error.status === 403) return "This operator key does not have permission for event operations.";
  if (error instanceof APIError && error.status === 404) return "This record is no longer retained. Refresh the list.";
  if (error instanceof APIError && error.status === 409) return "The record changed. Refresh its details before taking another action.";
  return error instanceof Error ? error.message : "Event operations are unavailable. Check core connectivity and try again.";
}
export function EventOperations({ session, identity, active }: { session: Session; identity: Identity; active: boolean }) {
  const [kind, setKind] = React.useState<Kind>("subscriptions");
  const [link, setLink] = React.useState<Link>();
  const navigate = (next: Omit<Link, "serial">) => { setKind(next.kind); setLink({ ...next, serial: Date.now() }); };
  return <div hidden={!active} className="event-operations">
    <Tabs value={kind} onChange={(v) => setKind(v as Kind)} className="record-navigation">
      <Tabs.List aria-label="MCP event views"><Tabs.Tab value="subscriptions">Subscriptions</Tabs.Tab><Tabs.Tab value="deliveries">Deliveries</Tabs.Tab></Tabs.List>
    </Tabs>
    {(["subscriptions", "deliveries"] as const).map((view) => <EventPane key={view} kind={view} active={active && kind === view} session={session} identity={identity} link={link} navigate={navigate} />)}
  </div>;
}
function EventPane({ kind, active, session, identity, link, navigate }: { kind: Kind; active: boolean; session: Session; identity: Identity; link?: Link; navigate: (next: Omit<Link, "serial">) => void }) {
  const [filters, setFilters] = React.useState<Filters>(emptyFilters);
  const [draft, setDraft] = React.useState<Filters>(emptyFilters);
  const [items, setItems] = React.useState<Detail[]>([]);
  const [selected, setSelected] = React.useState("");
  const [detail, setDetail] = React.useState<Detail>();
  const [cursor, setCursor] = React.useState("");
  const [pages, setPages] = React.useState<string[]>([]);
  const [next, setNext] = React.useState("");
  const [state, setState] = React.useState("loading");
  const [error, setError] = React.useState("");
  const [detailError, setDetailError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [refresh, setRefresh] = React.useState(0);
  const [payload, setPayload] = React.useState("");
  const [intent, setIntent] = React.useState<{ action: "retry" | "revoke" | "restore"; detail: Detail }>();
  const inbox = React.useRef<HTMLDivElement>(null);
  const heading = React.useRef<HTMLHeadingElement>(null);
  const scroll = React.useRef(0);
  const currentSelection = React.useRef(selected); currentSelection.current = selected;
  const focused = React.useRef("");
  const trigger = React.useRef<HTMLElement | null>(null);
  const payloadRequest = React.useRef<AbortController | undefined>(undefined);
  const canRead = identity.scopes.includes("events_read");
  const refreshNow = () => setRefresh((v) => v + 1);
  React.useEffect(() => {
    if (link?.kind !== kind) return;
    if (link.subscription !== undefined) { const f = { ...emptyFilters, subscription: link.subscription }; setFilters(f); setDraft(f); setCursor(""); setPages([]); }
    if (link.id) { setDetail(undefined); setSelected(link.id); }
  }, [link, kind]);
  React.useEffect(() => { setPayload(""); setDetailError(""); }, [selected]);
  React.useEffect(() => () => payloadRequest.current?.abort(), [selected, active, session]);
  React.useEffect(() => { if (detail?.id && focused.current !== detail.id) { focused.current = detail.id; heading.current?.focus(); } }, [detail]);
  React.useEffect(() => {
    if (!active || !canRead) return;
    let live = true; let running = false; let controller: AbortController | undefined; let timer: ReturnType<typeof setTimeout> | undefined;
    const abort = () => controller?.abort();
    session.signal.addEventListener("abort", abort);
    async function poll() {
      if (!live || running || session.signal.aborted || document.visibilityState !== "visible") return;
      running = true; controller = new AbortController(); const signal = controller.signal; setBusy(true);
      try {
        const status = result(await session.client.GET("/v1/mcp-events/status", { signal }));
        if (signal.aborted || !live) return;
        setState(status.state);
        if (status.state !== "enabled") return;
        const query = { limit: 50, ...Object.fromEntries(Object.entries(filters).filter(([, value]) => value)), ...(cursor ? { cursor } : {}) };
        const page = kind === "subscriptions" ? result(await session.client.GET("/v1/mcp-events/subscriptions", { params: { query }, signal })) : result(await session.client.GET("/v1/mcp-events/deliveries", { params: { query }, signal }));
        if (signal.aborted || !live) return;
        setItems(page.items); setNext(page.next_cursor || ""); setError("");
        if (selected) {
          try {
            const value = kind === "subscriptions" ? result(await session.client.GET("/v1/mcp-events/subscriptions/{id}", { params: { path: { id: selected } }, signal })) : result(await session.client.GET("/v1/mcp-events/deliveries/{id}", { params: { path: { id: selected } }, signal }));
            if (live && !signal.aborted) { setDetail(value); setDetailError(""); }
          } catch (caught) { if (live && !signal.aborted) { setDetail(undefined); setDetailError(errorText(caught)); } }
        }
      } catch (caught) { if (live && !signal.aborted) setError(errorText(caught)); }
      finally { running = false; if (live) { setBusy(false); if (document.visibilityState === "visible") timer = setTimeout(() => void poll(), 5000); } }
    }
    const visibility = () => { if (timer) clearTimeout(timer); if (document.visibilityState === "visible") void poll(); else abort(); };
    document.addEventListener("visibilitychange", visibility); void poll();
    return () => { live = false; abort(); if (timer) clearTimeout(timer); document.removeEventListener("visibilitychange", visibility); session.signal.removeEventListener("abort", abort); };
  }, [active, canRead, cursor, filters, kind, refresh, selected, session]);
  const select = (id: string) => { scroll.current = inbox.current?.scrollTop || 0; setSelected(id); setDetail(undefined); };
  const back = () => { setSelected(""); setDetail(undefined); focused.current = ""; requestAnimationFrame(() => { if (inbox.current) inbox.current.scrollTop = scroll.current; }); };
  const open = (action: "retry" | "revoke" | "restore") => { if (!detail) return; trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setIntent({ action, detail }); };
  const loadPayload = async () => {
    const id = selected;
    payloadRequest.current?.abort();
    const controller = new AbortController(); payloadRequest.current = controller;
    const signal = AbortSignal.any([session.signal, controller.signal]);
    try { const response = await session.client.GET("/v1/mcp-events/deliveries/{id}/payload", { params: { path: { id } }, signal, parseAs: "text" }); const text = result(response); if (id === currentSelection.current && !signal.aborted) setPayload(text); }
    catch (caught) { if (id === currentSelection.current && !signal.aborted) setDetailError(errorText(caught)); }
  };
  if (!active) return null;
  if (!canRead) return <Alert title="Permission required">This operator key needs events_read to inspect MCP events.</Alert>;
  return <section className={`console-investigation ${selected ? "has-detail" : ""}`} aria-label={`MCP ${kind}`}>
    <div className="console-workspace">
      <section className="failure-inbox">
        <div className="inbox-chrome">
          <div className="console-page-heading"><div><Title order={2} size="h3">{kind === "subscriptions" ? "Event subscriptions" : "Event deliveries"}</Title><Text size="sm" c="dimmed">{kind === "subscriptions" ? "Inspect monitoring and control future delivery." : "Inspect receipt evidence and recover failed callbacks."}</Text></div><Button size="xs" variant="default" leftSection={<FiRefreshCw />} onClick={refreshNow} disabled={busy}>Refresh</Button></div>
          <form className="console-filters" onSubmit={(e) => { e.preventDefault(); setFilters(draft); setCursor(""); setPages([]); }}>
            <div className="filter-toolbar"><TextInput size="xs" label="Endpoint" value={draft.endpoint} onChange={(e) => setDraft({ ...draft, endpoint: e.currentTarget.value })} /><TextInput size="xs" label="Event" value={draft.event} onChange={(e) => setDraft({ ...draft, event: e.currentTarget.value })} /><Select size="xs" label="State" value={draft.state} onChange={(v) => setDraft({ ...draft, state: v || "" })} data={[{ value: "", label: "All states" }, ...(kind === "subscriptions" ? ["active", "inactive", "expired", "revoked"] : ["pending", "delivered", "failed", "cancelled"]).map((value) => ({ value, label: value === "delivered" ? "Receipt accepted" : value }))]} /><Button size="xs" type="submit" variant="default">Apply filters</Button></div>
            <details className="event-advanced"><summary>More filters</summary><Stack gap="xs"><TextInput size="xs" label="Owner" value={draft.owner} onChange={(e) => setDraft({ ...draft, owner: e.currentTarget.value })} /><TextInput size="xs" label="Subscription ID" value={draft.subscription} onChange={(e) => setDraft({ ...draft, subscription: e.currentTarget.value })} />{(["since", "until"] as const).map((key) => <TextInput key={key} size="xs" label={`${key === "since" ? "Since" : "Until"} (${kind === "subscriptions" ? "expiry" : "acceptance"}, RFC3339)`} value={draft[key]} onChange={(e) => setDraft({ ...draft, [key]: e.currentTarget.value })} />)}</Stack></details>
          </form>
        </div>
        {error && <Alert color="red" title="Event request failed">{error}</Alert>}
        {state !== "enabled" && <Alert title={state === "loading" ? "Connecting to core" : state === "disabled" ? "Event delivery is disabled" : state === "unsupported" ? "Core does not support event operations" : "Event store is unavailable"}>{state === "disabled" ? "Configure event delivery in core to enable this view." : "Check the core version, event-store migration, and private control connection. Existing replay operations remain available."}</Alert>}
        <div className="inbox-scroll" ref={inbox} aria-busy={busy}>
          {state === "enabled" && items.length === 0 && !busy && !error && <EmptyState><strong>No matching records.</strong> Change the filters or create a subscription and send a matching event.</EmptyState>}
          {items.map((item) => <button key={item.id} type="button" className={`event-inbox-row ${selected === item.id ? "is-selected" : ""}`} onClick={() => select(item.id)} aria-pressed={selected === item.id}><span><strong>{item.event}</strong><Badge variant="light" color={item.state === "failed" || item.state === "revoked" ? "red" : "gray"}>{item.state === "delivered" ? "Receipt accepted" : item.state}</Badge></span><Text size="xs" c="dimmed" component="span">{item.endpoint} · {item.owner}</Text><code>{item.id}</code></button>)}
        </div>
        <Group className="inbox-footer" justify="space-between"><Button size="xs" variant="default" disabled={!pages.length} onClick={() => { setCursor(pages[pages.length - 1]); setPages(pages.slice(0, -1)); }}>Previous</Button><Text size="xs" c="dimmed">{items.length} records</Text><Button size="xs" variant="default" disabled={!next} onClick={() => { setPages([...pages, cursor]); setCursor(next); }}>Next</Button></Group>
      </section>
      <section className="detail-panel" aria-label="Event evidence">
        {selected && <div className="detail-navigation"><Button size="xs" variant="subtle" leftSection={<FiArrowLeft />} onClick={back}>Back to inbox</Button></div>}
        {detailError && <Alert color="red" title="Could not load evidence">{detailError}</Alert>}
        {!selected ? <EmptyState><strong>Select a record.</strong> Inspect its state and evidence before choosing an action.</EmptyState> : !detail && !detailError ? <LoadingState>Loading evidence…</LoadingState> : !detail ? null : <>
          <div className="detail-header"><Title order={2} ref={heading} tabIndex={-1}>{detail.event}</Title><Text size="sm" c="dimmed">{detail.state === "delivered" ? "Receipt accepted" : detail.state} · {detail.endpoint}</Text><Group mt="sm">{delivery(detail) ? <Button size="xs" disabled={!identity.scopes.includes("events_retry") || !detail.retry.allowed} title={detail.retry.reason || "events_retry permission required"} onClick={() => open("retry")}>Retry delivery</Button> : <><Button size="xs" color="red" variant="light" disabled={!identity.scopes.includes("events_manage") || !detail.revoke.allowed} onClick={() => open("revoke")}>Revoke subscription</Button><Button size="xs" variant="default" disabled={!identity.scopes.includes("events_manage") || !detail.restore.allowed} onClick={() => open("restore")}>Restore subscription</Button></>}</Group>{delivery(detail) ? !identity.scopes.includes("events_retry") && <Text size="xs" c="dimmed" mt="xs">Retry requires events_retry permission.</Text> : <Text size="xs" c="dimmed" mt="xs">{!identity.scopes.includes("events_manage") ? "Revoke and restore require events_manage permission." : detail.revoked_at ? detail.revoke.reason : detail.restore.reason || "Restoration is available after revocation."}</Text>}</div>
          <div className="event-evidence">
            <dl className="event-metadata"><dt>ID</dt><dd>{detail.id}</dd><dt>Owner</dt><dd>{detail.owner}</dd>{delivery(detail) ? <><dt>Event ID</dt><dd>{detail.event_id}</dd><dt>Subscription</dt><dd><Button size="xs" variant="subtle" onClick={() => navigate({ kind: "subscriptions", id: detail.subscription_id })}>{detail.subscription_id}</Button></dd><dt>Attempts</dt><dd>{detail.attempts} lifetime · {detail.cycle_attempts} in cycle {detail.cycle}</dd><dt>Next attempt</dt><dd>{detail.state === "pending" ? timestamp(detail.available_at) : "—"}</dd></> : <><dt>Callback origin</dt><dd>{detail.callback_origin}</dd><dt>Expires</dt><dd>{timestamp(detail.expires_at)}</dd><dt>Filters</dt><dd><pre>{detail.arguments_json}</pre></dd>{detail.revoked_at && <><dt>Revoked</dt><dd>{timestamp(detail.revoked_at)} · {detail.revoked_by}<p>{detail.reason}</p></dd></>}</>}</dl>
            {!delivery(detail) ? <><Button variant="default" size="xs" onClick={() => navigate({ kind: "deliveries", subscription: detail.id })}>View related deliveries</Button><Title order={3} size="h5" mt="lg">Operator actions</Title>{detail.actions.length ? detail.actions.map((action) => <Text size="sm" key={`${action.at}:${action.action}`}>{action.action} · {action.actor} · {timestamp(action.at)}{action.reason && ` · ${action.reason}`}</Text>) : <Text size="sm" c="dimmed">No retained operator actions.</Text>}</> : <>
              {detail.retry.reason && <Text size="sm" c="dimmed">{detail.retry.reason}</Text>}
              <Title order={3} size="h5" mt="lg">Delivery attempts</Title>
              {detail.history.length < detail.attempts && <Text size="sm" c="dimmed">Earlier attempts were not recorded or are outside the latest 200 entries.</Text>}
              <Table.ScrollContainer minWidth={420}><Table><Table.Thead><Table.Tr><Table.Th>Attempt</Table.Th><Table.Th>Started</Table.Th><Table.Th>HTTP</Table.Th><Table.Th>Outcome</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{detail.history.map((attempt) => <Table.Tr key={attempt.number}><Table.Td>{attempt.number} · cycle {attempt.cycle}</Table.Td><Table.Td>{timestamp(attempt.started_at)}</Table.Td><Table.Td>{attempt.http_status ?? "—"}</Table.Td><Table.Td>{attempt.outcome === "accepted" ? "Receipt accepted" : attempt.outcome}{attempt.duration_ms != null && <Text size="xs" c="dimmed">{attempt.duration_ms} ms</Text>}</Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer>
              <Text size="sm" c="dimmed">A callback 2xx acknowledges receipt. It does not confirm completion of an agent action.</Text>
              <Title order={3} size="h5" mt="lg">Operator actions</Title>{detail.actions.length ? detail.actions.map((action) => <Text size="sm" key={`${action.at}:${action.action}`}>{action.action} · {action.actor} · {timestamp(action.at)}</Text>) : <Text size="sm" c="dimmed">No retained operator actions.</Text>}
              <Button mt="lg" variant="default" size="xs" disabled={!identity.scopes.includes("events_payload")} onClick={() => void loadPayload()}>Load event payload</Button>{!identity.scopes.includes("events_payload") && <Text size="xs" c="dimmed">Payload access requires events_payload permission.</Text>}{payload && <pre className="event-payload">{payload}</pre>}
            </>}
          </div>
        </>}
      </section>
    </div>
    {intent && <EventAction key={`${intent.action}:${intent.detail.id}`} intent={intent} session={session} onDone={refreshNow} onClose={() => { setIntent(undefined); trigger.current?.focus(); }} />}
  </section>;
}
function EventAction({ intent, session, onDone, onClose }: { intent: { action: "retry" | "revoke" | "restore"; detail: Detail }; session: Session; onDone: () => void; onClose: () => void }) {
  const [reason, setReason] = React.useState(""); const [ack, setAck] = React.useState(false); const [busy, setBusy] = React.useState(false); const [error, setError] = React.useState("");
  const [submission, setSubmission] = React.useState<{ key: string; version: string; reason: string; acknowledge_duplicate_risk: boolean }>();
  const [uncertain, setUncertain] = React.useState(false);
  const submit = async () => {
    if (busy) return;
    const saved = submission || { key: crypto.randomUUID(), version: intent.detail.version, reason, acknowledge_duplicate_risk: ack }; setSubmission(saved); setBusy(true); setError("");
    const options = { params: { path: { id: intent.detail.id }, header: { "Idempotency-Key": saved.key } }, body: { version: saved.version, reason: saved.reason, acknowledge_duplicate_risk: saved.acknowledge_duplicate_risk }, signal: session.signal };
    let mutating = false;
    try {
      const validationInput = { action: `event_${intent.action}`, idempotency_key: saved.key, version: saved.version, note: saved.reason, acknowledge_duplicate_risk: saved.acknowledge_duplicate_risk };
      const validation: components["schemas"]["ActionValidationResult"] = hasWasmCapability("mcp_event_action_input") ? JSON.parse(invokeWasm("mcp_event_action_input", JSON.stringify(validationInput))) : result(await session.client.POST("/v1/actions/validate", { body: validationInput, signal: session.signal }));
      if (validation.issues.length) { setError(validation.issues.map((issue) => issue.message).join(" ")); setSubmission(undefined); return; }
      mutating = true;
      if (intent.action === "retry") result(await session.client.POST("/v1/mcp-events/deliveries/{id}/retry", options));
      else if (intent.action === "revoke") result(await session.client.POST("/v1/mcp-events/subscriptions/{id}/revoke", options));
      else result(await session.client.POST("/v1/mcp-events/subscriptions/{id}/restore", options));
      onDone(); onClose();
    } catch (caught) { if (!session.signal.aborted) { setError(errorText(caught)); const lost = mutating && (!(caught instanceof APIError) || caught.status >= 500); setUncertain(uncertain || lost); if (!uncertain && !lost) setSubmission(undefined); } }
    finally { setBusy(false); }
  };
  return <Modal opened title={intent.action === "retry" ? "Retry event delivery" : intent.action === "revoke" ? "Revoke event subscription" : "Restore event subscription"} onClose={onClose} closeOnClickOutside={!busy} closeOnEscape={!busy} withCloseButton={!busy}>
    <Stack>{intent.action === "retry" ? <><Text size="sm">Send the original event to its original subscriber in a new bounded retry cycle. The receiver may already have processed it.</Text><Checkbox label="I acknowledge that this may deliver a duplicate event" checked={ack} disabled={busy || !!submission} onChange={(e) => setAck(e.currentTarget.checked)} /></> : <><Text size="sm">{intent.action === "revoke" ? "Stop future delivery and block refreshes for this subscription identity until an operator restores it. Already-started callbacks may finish." : "Allow the agent to subscribe again. This will not reactivate monitoring or recover cancelled deliveries."}</Text><Textarea label="Reason" required maxLength={4096} value={reason} disabled={busy || !!submission} onChange={(e) => setReason(e.currentTarget.value)} /></>}
      {error && <Alert color="red" title={uncertain ? "Outcome not confirmed" : "Action rejected"}>{error}{uncertain && <Text size="sm">Keep the same request key when checking this action again.</Text>}</Alert>}
      <Group justify="flex-end"><Button variant="default" disabled={busy} onClick={onClose}>Close</Button><Button loading={busy} disabled={intent.action === "retry" ? !ack : !reason.trim()} onClick={() => void submit()}>{uncertain ? "Resubmit same request" : "Confirm"}</Button></Group>
    </Stack>
  </Modal>;
}
