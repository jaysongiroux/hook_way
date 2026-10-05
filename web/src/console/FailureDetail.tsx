import React from "react";
import { FailureState } from "./FailureState";
import { FiCopy, FiCheck } from "react-icons/fi";
import {
  Alert,
  Button,
  Code,
  Group,
  Modal,
  Stack,
  Text,
  Title,
  Tabs,
} from "@mantine/core";
import {
  result,
  requestError,
  timestamp,
  type Detail,
  type Session,
} from "./session";
import type { Intent } from "./ActionDialog";

const PREVIEW_BYTES = 64 * 1024;
export function FailureDetail({
  record,
  session,
  scopes,
  onSelect,
  onAction,
  actionPending,
}: {
  record: Detail;
  session: Session;
  scopes: string[];
  onSelect: (id: string) => void;
  onAction: (intent: Intent) => void;
  actionPending: boolean;
}) {
  const [tab, setTab] = React.useState<string | null>("overview");
  const [copyState, setCopyState] = React.useState("");
  const [preview, setPreview] = React.useState<string>();
  const [payloadError, setPayloadError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [truncated, setTruncated] = React.useState(false);
  const mounted = React.useRef(true);
  React.useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const payload = record.artifacts.payload;
  const canPayload =
    scopes.includes("payload") &&
    record.artifacts.stored &&
    !!payload &&
    new Date(record.expires_at).getTime() > Date.now();
  const canOperate = scopes.includes("retry");
  async function readPayload(download: boolean) {
    setBusy(true);
    setPayloadError("");
    try {
      const response = await session.client.GET("/v1/failures/{id}/payload", {
        params: { path: { id: record.id } },
        parseAs: "stream",
        signal: session.signal,
      });
      const stream = result(response);
      if (!stream) throw new Error("The payload response was empty.");
      if (download) {
        const blob = await new Response(stream).blob();
        if (!mounted.current || session.signal.aborted) return;
        const url = URL.createObjectURL(
          new Blob([blob], { type: "application/octet-stream" }),
        );
        const anchor = document.createElement("a");
        anchor.href = url;
        anchor.download =
          (payload?.filename || `${record.id}.bin`)
            .split(/[\\/]/)
            .pop()!
            .replace(/[\x00-\x1f\x7f]/g, "_") || "payload.bin";
        anchor.click();
        URL.revokeObjectURL(url);
      } else {
        const reader = stream.getReader();
        const bytes: Uint8Array[] = [];
        let size = 0;
        let clipped = false;
        try {
          while (size < PREVIEW_BYTES) {
            const next = await reader.read();
            if (next.done) break;
            const part = next.value.subarray(0, PREVIEW_BYTES - size);
            bytes.push(part);
            size += part.length;
            if (size === PREVIEW_BYTES) {
              clipped = (payload?.size ?? 0) > PREVIEW_BYTES;
              break;
            }
          }
        } finally {
          await reader.cancel();
        }
        const combined = new Uint8Array(size);
        let offset = 0;
        for (const part of bytes) {
          combined.set(part, offset);
          offset += part.length;
        }
        if (combined.includes(0))
          throw new Error(
            "Binary payload: download the original bytes to inspect them.",
          );
        let text: string;
        try {
          text = new TextDecoder("utf-8", { fatal: true }).decode(combined, {
            stream: clipped,
          });
        } catch {
          throw new Error(
            "This payload is not UTF-8 text. Download the original bytes.",
          );
        }
        if (!mounted.current || session.signal.aborted) return;
        setPreview(text);
        setTruncated(clipped);
      }
    } catch (caught) {
      if (mounted.current && !session.signal.aborted)
        setPayloadError(requestError(caught, "Payload could not be loaded."));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }
  const latest = record.runs[0];
  const explanation =
    record.status === "unknown"
      ? "Delivery may have happened, but Hookway cannot confirm it. Check the destination's receipt or audit trail before retrying or confirming the outcome."
      : latest?.status === "blocked"
        ? "The retry was blocked before delivery. Inspect the run result and restore the missing configuration, credentials or control access before trying again."
        : record.status === "open"
          ? "The delivery failed. Inspect the error and current destination before requesting another attempt."
          : record.status === "retrying"
            ? "A retry is queued or running. Wait for its outcome; acceptance alone does not confirm delivery."
            : record.status === "dismissed"
              ? "Recovery was stopped and stored artifacts were deleted. Metadata remains until retention expires."
              : record.resolution_source === "operator_confirmed"
                ? "An operator confirmed delivery using external evidence. The original unknown run remains unchanged."
                : "A retry succeeded. Inspect any child failures for downstream work that still needs attention.";
  const payloadControls = (
    <section className="payload-section" aria-label="Captured payload">
      <Group justify="space-between" mb="xs">
        <Title order={3} size="h5">
          Payload
        </Title>
        <Text size="xs" c="dimmed">
          {payload ? `${payload.size.toLocaleString()} bytes` : "Not captured"}
        </Text>
      </Group>
      <Text size="sm" c="dimmed" mb="sm">
        {payload
          ? payload.content_type || "Content type not recorded"
          : "No payload was captured."}
      </Text>
      {canPayload ? (
        <Group gap="xs">
          <Button
            size="xs"
            variant="default"
            loading={busy}
            onClick={() => void readPayload(false)}
          >
            Preview payload
          </Button>
          <Button
            size="xs"
            variant="subtle"
            disabled={busy}
            onClick={() => void readPayload(true)}
          >
            Download original bytes
          </Button>
        </Group>
      ) : (
        <Text size="sm" c="dimmed">
          {!scopes.includes("payload")
            ? "A key with payload scope is required to view captured bytes."
            : "Captured bytes are unavailable or retention has expired."}
        </Text>
      )}
      {payloadError && (
        <Alert mt="sm" color="red" title="Payload unavailable">
          {payloadError}
        </Alert>
      )}
    </section>
  );
  return (
    <div className="failure-detail">
      <div className="detail-header">
        <Group justify="space-between" align="start" gap="sm">
          <div className="detail-title">
            <Title order={2} size="h3">
              {record.destination_id || record.node_id}
            </Title>
            <Text size="sm" c="dimmed">
              {record.flow} / {record.node_id}
            </Text>
          </div>
          <FailureState record={record} />
        </Group>
        <details className="outcome-guidance">
          <summary>Delivery guidance</summary>
          <Text size="sm">{explanation}</Text>
        </details>
        {canOperate ? (
          <>
            <Group gap="xs" className="recovery-actions">
              <Button
                size="sm"
                disabled={!record.actions.retry.allowed || actionPending}
                onClick={() => onAction({ action: "retry", records: [record] })}
              >
                Retry delivery
              </Button>
              {record.actions.resolve.allowed && (
                <Button
                  size="sm"
                  variant="default"
                  disabled={actionPending}
                  onClick={() =>
                    onAction({ action: "resolve", records: [record] })
                  }
                >
                  Confirm outcome
                </Button>
              )}
              <Button
                size="sm"
                variant="subtle"
                color="red.9"
                c="var(--mantine-color-red-text)"
                disabled={!record.actions.dismiss.allowed || actionPending}
                onClick={() =>
                  onAction({ action: "dismiss", records: [record] })
                }
              >
                Dismiss failure
              </Button>
            </Group>
            {!record.actions.retry.allowed && (
              <Text size="xs" c="dimmed" mt="xs">
                Retry unavailable: {record.actions.retry.reason}
              </Text>
            )}
          </>
        ) : (
          <Text size="xs" c="dimmed">
            Read-only access. A key with retry scope is required for recovery
            actions.
          </Text>
        )}
      </div>
      <Tabs
        value={tab}
        onChange={setTab}
        className="evidence-tabs"
        keepMounted={false}
      >
        <Tabs.List aria-label="Delivery evidence">
          <Tabs.Tab value="overview">Overview</Tabs.Tab>
          <Tabs.Tab value="activity">Activity</Tabs.Tab>
          <Tabs.Tab value="related">
            Related
            {record.children.length ? ` (${record.children.length})` : ""}
          </Tabs.Tab>
        </Tabs.List>
        <div className="evidence-scroll">
          <Tabs.Panel value="overview">
            <Stack gap="lg" className="evidence-content">
              <Alert
                color={record.status === "unknown" ? "yellow" : "gray"}
                title={
                  record.status === "unknown"
                    ? "Delivery is not confirmed"
                    : `Captured error · ${record.error_class}`
                }
              >
                <Text size="sm">
                  {record.error_message ||
                    "No additional error message was captured."}
                </Text>
              </Alert>
              {record.resolution && (
                <Alert title="Resolved — operator confirmed" color="green">
                  <Text size="sm">
                    {record.resolution.actor} ·{" "}
                    {timestamp(record.resolution.resolved_at)}
                  </Text>
                  <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                    {record.resolution.note}
                  </Text>
                  {record.resolution.reference && (
                    <Text size="sm">
                      Reference: {record.resolution.reference}
                    </Text>
                  )}
                </Alert>
              )}
              {latest && (
                <section className="latest-run">
                  <Title order={3} size="h5" mb="xs">
                    Latest retry
                  </Title>
                  <Text size="sm" fw={600}>
                    {latest.status === "unknown"
                      ? "Unknown — delivery not confirmed"
                      : latest.status === "blocked"
                        ? "Blocked — delivery not attempted"
                        : latest.status}
                  </Text>
                  {latest.result != null && (
                    <Diagnostic value={latest.result} />
                  )}
                  {record.runs.length > 0 && (
                    <Button
                      className="activity-link"
                      size="xs"
                      variant="subtle"
                      onClick={() => setTab("activity")}
                    >
                      View retry history
                    </Button>
                  )}
                </section>
              )}
              <section aria-label="Delivery information">
                <Title order={3} size="h5" mb="sm">
                  Delivery information
                </Title>
                <dl className="delivery-metadata">
                  <div className="metadata-wide">
                    <dt>Target</dt>
                    <dd className="identifier">
                      {record.rendered_target || record.target || "—"}
                    </dd>
                  </div>
                  <div>
                    <dt>Retry scope</dt>
                    <dd>
                      {record.kind === "publish_only"
                        ? "Retry scope: captured destination only"
                        : "Retry scope: resume at the captured node"}
                    </dd>
                  </div>
                  <div>
                    <dt>Attempts</dt>
                    <dd>{record.attempts}</dd>
                  </div>
                  <div>
                    <dt>Failed at</dt>
                    <dd>{timestamp(record.failed_at)}</dd>
                  </div>
                  <div>
                    <dt>Retention expires</dt>
                    <dd>{timestamp(record.expires_at)}</dd>
                  </div>
                  <div>
                    <dt>Event ID</dt>
                    <dd className="identifier">
                      {record.event_id || "Not recorded"}
                    </dd>
                  </div>
                  <div>
                    <dt>Destination type</dt>
                    <dd>{record.destination_type || record.node_type}</dd>
                  </div>
                  <div className="metadata-wide">
                    <dt>Failure ID</dt>
                    <dd className="failure-id-line">
                      <span className="identifier">{record.id}</span>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        aria-label="Copy failure ID"
                        leftSection={
                          copyState === "Copied" ? (
                            <FiCheck aria-hidden="true" />
                          ) : (
                            <FiCopy aria-hidden="true" />
                          )
                        }
                        onClick={() => {
                          void navigator.clipboard.writeText(record.id).then(
                            () => setCopyState("Copied"),
                            () =>
                              setCopyState(
                                "Copy failed; select the ID to copy it.",
                              ),
                          );
                        }}
                      >
                        {copyState === "Copied" ? "Copied" : "Copy"}
                      </Button>
                    </dd>
                  </div>
                </dl>
                {copyState.startsWith("Copy failed") && (
                  <Text size="xs" role="alert">
                    {copyState}
                  </Text>
                )}
              </section>
              {payloadControls}
            </Stack>
          </Tabs.Panel>
          <Tabs.Panel value="activity">
            <Stack gap="lg" className="evidence-content">
              <section>
                <Title order={3} size="h5" mb="sm">
                  Retry history
                </Title>
                {!record.runs.length && (
                  <Text size="sm" c="dimmed">
                    No retry has been requested.
                  </Text>
                )}
                <ol className="activity-timeline">
                  {record.runs.map((run) => (
                    <li key={run.run_id}>
                      <Group justify="space-between" gap="xs">
                        <Text fw={600} size="sm">
                          {run.status === "unknown"
                            ? "Unknown — delivery not confirmed"
                            : run.status === "blocked"
                              ? "Blocked — delivery not attempted"
                              : run.status}
                        </Text>
                        <Text size="xs" c="dimmed">
                          {timestamp(run.created_at)}
                        </Text>
                      </Group>
                      <Text size="xs" c="dimmed">
                        Requested by {run.requested_by}
                      </Text>
                      <details className="diagnostic-details">
                        <summary>Run identifiers</summary>
                        <Text size="xs" className="identifier">
                          Run: {run.run_id}
                        </Text>
                        <Text size="xs" className="identifier">
                          Submission key: {run.idempotency_key}
                        </Text>
                      </details>
                      {run.result != null && <Diagnostic value={run.result} />}
                    </li>
                  ))}
                </ol>
              </section>
              <section>
                <Title order={3} size="h5" mb="sm">
                  Audit history
                </Title>
                <ol className="activity-timeline">
                  {record.history.map((event, index) => (
                    <li key={`${event.at}-${index}`}>
                      <Text size="sm" fw={600}>
                        {event.event.replace(/_/g, " ")}
                      </Text>
                      <Text size="xs" c="dimmed">
                        {event.actor} · {timestamp(event.at)}
                      </Text>
                      {event.detail != null && (
                        <Diagnostic value={event.detail} />
                      )}
                    </li>
                  ))}
                </ol>
              </section>
            </Stack>
          </Tabs.Panel>
          <Tabs.Panel value="related">
            <Stack gap="md" className="evidence-content">
              <Title order={3} size="h5">
                Related failures
              </Title>
              <Text size="sm" c="dimmed">
                Follow the captured delivery relationships. A successful retry
                can still create a downstream failure.
              </Text>
              <Group gap="xs">
                {record.parent_id && (
                  <Button
                    variant="default"
                    size="sm"
                    onClick={() => onSelect(record.parent_id!)}
                  >
                    Inspect parent
                  </Button>
                )}
                {record.root_id !== record.id &&
                  record.root_id !== record.parent_id && (
                    <Button
                      variant="default"
                      size="sm"
                      onClick={() => onSelect(record.root_id)}
                    >
                      Inspect root
                    </Button>
                  )}
                {record.children.map((id) => (
                  <Button
                    key={id}
                    variant="default"
                    size="sm"
                    className="related-failure"
                    onClick={() => onSelect(id)}
                  >
                    Inspect child {id}
                  </Button>
                ))}
              </Group>
              {!record.parent_id && !record.children.length && (
                <Text size="sm" c="dimmed">
                  No related failures.
                </Text>
              )}
            </Stack>
          </Tabs.Panel>
        </div>
      </Tabs>
      <Modal
        opened={preview !== undefined}
        onClose={() => setPreview(undefined)}
        title="Captured payload — escaped UTF-8 text"
        size="xl"
      >
        <Stack>
          <Text size="sm">
            Exact text; JSON numbers are preserved without parsing or rounding.{" "}
            {truncated &&
              "Preview is limited to the first 64 KiB. Download the original for the complete payload."}
          </Text>
          <pre className="payload-preview">{preview}</pre>
        </Stack>
      </Modal>
    </div>
  );
}

function Diagnostic({ value }: { value: unknown }) {
  const fields =
    value && typeof value === "object"
      ? (value as Record<string, unknown>)
      : undefined;
  const detail = fields?.detail;
  const reason = fields?.reason;
  return (
    <div className="diagnostic">
      <Text size="sm">
        {typeof detail === "string"
          ? detail
          : typeof reason === "string"
            ? reason.replace(/_/g, " ")
            : ""}
      </Text>
      <details className="diagnostic-details">
        <summary>Raw diagnostic data</summary>
        <Code block>{JSON.stringify(value, null, 2)}</Code>
      </details>
    </div>
  );
}
