import React from "react";
import {
  Alert,
  Button,
  Checkbox,
  Group,
  Modal,
  Stack,
  Text,
  Textarea,
  TextInput,
} from "@mantine/core";
import { hasWasmCapability, invokeWasm } from "../shared/wasm/loader";
import type { components } from "../shared/contracts/generated/replay";
import {
  APIError,
  requestError,
  result,
  statusLabel,
  type ReplayRecord,
  type Session,
} from "./session";

type Action = "retry" | "resolve" | "dismiss";
export interface Intent {
  action: Action;
  records: ReplayRecord[];
}
type Submission = {
  key: string;
  note: string;
  reference: string;
  ack: boolean;
};

export function ActionDialog({
  intent,
  session,
  opened,
  onClose,
  onRefresh,
}: {
  intent: Intent;
  session: Session;
  opened: boolean;
  onClose: (retain: boolean, completed: boolean) => void;
  onRefresh: () => void;
}) {
  const [ack, setAck] = React.useState(false);
  const [confirmed, setConfirmed] = React.useState(false);
  const [note, setNote] = React.useState("");
  const [reference, setReference] = React.useState("");
  const [submission, setSubmission] = React.useState<Submission>();
  const [busy, setBusy] = React.useState(false);
  const [uncertain, setUncertain] = React.useState(false);
  const [error, setError] = React.useState("");
  const [inputIssues, setInputIssues] = React.useState<
    components["schemas"]["ActionIssue"][]
  >([]);
  const noteRef = React.useRef<HTMLTextAreaElement>(null);
  const referenceRef = React.useRef<HTMLInputElement>(null);
  const [outcomes, setOutcomes] = React.useState<string[]>();
  const [apiValidation, setAPIValidation] = React.useState(false);
  const { action, records } = intent;
  const inspection = records.every((record) => record.kind === "inspection");
  const ids = records.map((record) => record.id);
  const needsAck =
    action === "retry" &&
    records.some(
      (record) => record.actions.retry.requires_duplicate_acknowledgement,
    );
  const capability =
    action === "resolve" ? "replay_resolution_input" : "replay_action_input";
  const local = hasWasmCapability(capability);
  const frozen = busy || uncertain || !!outcomes;
  const title =
    action === "retry"
      ? `Retry ${records.length === 1 ? "delivery" : `${records.length} deliveries`}`
      : action === "resolve"
        ? "Confirm delivery outcome"
        : inspection ? "Dismiss inspection" : "Dismiss failure";

  async function submit() {
    if (busy) return;
    const saved = submission ?? {
      key: crypto.randomUUID(),
      note,
      reference,
      ack,
    };
    setSubmission(saved);
    setBusy(true);
    setError("");
    setInputIssues([]);
    let mutating = false;
    try {
      if (action !== "dismiss") {
        const input = {
          action,
          idempotency_key: saved.key,
          ...(action === "resolve"
            ? { note: saved.note, reference: saved.reference }
            : { record_ids: ids }),
        };
        let validation: components["schemas"]["ActionValidationResult"];
        if (local)
          validation = JSON.parse(
            invokeWasm(capability, JSON.stringify(input)),
          );
        else if (apiValidation)
          validation = result(
            await session.client.POST("/v1/actions/validate", {
              body: input,
              signal: session.signal,
            }),
          );
        else
          throw new Error(
            "Choose API input validation or restore local validation before submitting.",
          );
        if (validation.issues.length) {
          setInputIssues(validation.issues);
          setSubmission(undefined);
          requestAnimationFrame(() => {
            const field = validation.issues[0].field;
            if (field === "note") noteRef.current?.focus();
            else if (field === "reference") referenceRef.current?.focus();
          });
          return;
        }
      }
      mutating = true;
      const headers = { "Idempotency-Key": saved.key };
      if (action === "resolve") {
        result(
          await session.client.POST("/v1/records/{id}/resolve", {
            params: { path: { id: ids[0] }, header: headers },
            body: { note: saved.note, reference: saved.reference },
            signal: session.signal,
          }),
        );
        setOutcomes([
          "Resolved — operator confirmed. The original uncertain run and its evidence remain in history.",
        ]);
      } else if (action === "dismiss") {
        result(
          await session.client.POST("/v1/records/{id}/dismiss", {
            params: { path: { id: ids[0] } },
            signal: session.signal,
          }),
        );
        setOutcomes([
          "Dismissed. Stored payload and snapshot have been deleted.",
        ]);
      } else if (ids.length === 1) {
        const response = result(
          await session.client.POST("/v1/records/{id}/retries", {
            params: { path: { id: ids[0] }, header: headers },
            body: { acknowledge_duplicate_risk: saved.ack },
            signal: session.signal,
          }),
        );
        setOutcomes([
          `Queued retry ${response.run_id}. Acceptance does not confirm delivery; inspect run history for its outcome.`,
        ]);
      } else {
        const response = result(
          await session.client.POST("/v1/retries", {
            params: { header: headers },
            body: { record_ids: ids, acknowledge_duplicate_risk: saved.ack },
            signal: session.signal,
          }),
        );
        setOutcomes(
          response.results.map(
            (item) =>
              `${item.record_id}: ${item.status === 202 ? `queued ${item.run_id}` : `${item.code} — ${item.detail}`}`,
          ),
        );
      }
      setUncertain(false);
      onRefresh();
    } catch (caught) {
      if (session.signal.aborted) return;
      const message = requestError(caught, "Request failed.");
      // Lost responses and server errors may follow a committed transaction.
      const lost =
        mutating && (!(caught instanceof APIError) || caught.status >= 500);
      // Rejecting a repeat does not disprove acceptance of the original request.
      const stillUncertain = uncertain || lost;
      setUncertain(stillUncertain);
      setError(message);
      if (!stillUncertain) {
        setSubmission(undefined);
        onRefresh();
      }
    } finally {
      setBusy(false);
    }
  }
  async function checkDismissal() {
    setBusy(true);
    try {
      const record = result(
        await session.client.GET("/v1/records/{id}", {
          params: { path: { id: ids[0] } },
          signal: session.signal,
        }),
      );
      if (!!record.dismissed_at) {
        setOutcomes(["Dismissal confirmed by current API state."]);
        setUncertain(false);
      } else {
        setError(
          `Current state: ${statusLabel(record)}. Dismissal is not confirmed. Inspect the record before making another decision.`,
        );
      }
      onRefresh();
    } catch (caught) {
      if (session.signal.aborted) return;
      setError(requestError(caught, "Could not check the record."));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      opened={opened}
      returnFocus={false}
      onClose={() => onClose(uncertain, !!outcomes)}
      title={title}
      size="lg"
      closeOnClickOutside={!frozen}
      closeOnEscape={!frozen}
      withCloseButton={!busy && !uncertain}
    >
      <Stack>
        <Text size="sm">
          {action === "retry"
            ? "Only these captured records will be retried. Publish failures retry the selected destination; resume failures continue from the captured node. Successful earlier work is not rerun."
            : action === "resolve"
              ? "Confirm only after checking the destination's receipt or audit trail. This records your conclusion; it does not prove delivery or erase the unknown result."
              : inspection ? "Dismissal permanently deletes this inspection’s stored body and context. Metadata and audit history remain until retention expires." : "Dismissal stops recovery for this record and permanently deletes its stored payload and snapshot. Metadata and history remain until retention expires."}
        </Text>
        <div className="action-records" aria-label={inspection ? "Selected inspections" : "Selected deliveries"}>
          {records.map((record) => (
            <div className="action-record" key={record.id}>
              <Text size="sm" fw={600}>
                {record.destination_id || record.node_id}
              </Text>
              <Text size="xs" c="dimmed">
                {record.flow} / {record.node_id}
              </Text>
              <Text size="xs" className="identifier">
                {record.id}
              </Text>
            </div>
          ))}
        </div>
        {action === "resolve" && (
          <>
            <Textarea
              ref={noteRef}
              data-autofocus
              label="Evidence note"
              description="What you checked and how it confirms delivery; up to 4096 UTF-8 bytes."
              value={note}
              onChange={(e) => {
                setNote(e.currentTarget.value);
                setInputIssues((issues) =>
                  issues.filter((issue) => issue.field !== "note"),
                );
              }}
              error={
                inputIssues.find((issue) => issue.field === "note")?.message
              }
              disabled={frozen}
              minRows={3}
              required
            />
            <TextInput
              ref={referenceRef}
              label="Receipt or incident reference"
              description="Optional reference; stored as text, never opened automatically."
              value={reference}
              onChange={(e) => {
                setReference(e.currentTarget.value);
                setInputIssues((issues) =>
                  issues.filter((issue) => issue.field !== "reference"),
                );
              }}
              error={
                inputIssues.find((issue) => issue.field === "reference")
                  ?.message
              }
              disabled={frozen}
            />
          </>
        )}
        {needsAck && (
          <Checkbox
            data-autofocus
            label="I understand the destination may already have received this delivery and another retry can create a duplicate."
            checked={ack}
            onChange={(e) => setAck(e.currentTarget.checked)}
            disabled={frozen}
          />
        )}
        {action === "dismiss" && (
          <Checkbox
            data-autofocus
            label={inspection ? "I understand the captured body and context will be deleted." : "I understand the stored payload and snapshot will be deleted."}
            checked={confirmed}
            onChange={(e) => setConfirmed(e.currentTarget.checked)}
            disabled={frozen}
          />
        )}
        {action !== "dismiss" && !local && (
          <Alert color="yellow" title="Local input validation is unavailable">
            <Stack gap="xs">
              <Text size="sm">
                The replay API supports the same pure input checks. Permission
                and current record state are checked separately when the action
                runs.
              </Text>
              <Checkbox
                label="Use API input validation for this request"
                checked={apiValidation}
                onChange={(e) => setAPIValidation(e.currentTarget.checked)}
                disabled={frozen}
              />
            </Stack>
          </Alert>
        )}
        {inputIssues.length > 0 && (
          <Alert color="red" title="Check the highlighted fields">
            <ul className="validation-errors">
              {inputIssues.map((issue, index) => (
                <li key={`${issue.field}-${index}`}>
                  <strong>
                    {issue.field === "note"
                      ? "Evidence note"
                      : issue.field === "reference"
                        ? "Receipt or incident reference"
                        : "Action input"}
                    :
                  </strong>{" "}
                  {issue.message}
                </li>
              ))}
            </ul>
          </Alert>
        )}
        {error && (
          <Alert
            color={uncertain ? "yellow" : "red"}
            title={
              uncertain
                ? "Response uncertain — the action may have been accepted"
                : "Action was not confirmed"
            }
          >
            {error}
            {uncertain && (
              <Text size="sm" mt="xs">
                Check history.{" "}
                {action === "dismiss"
                  ? "Use the current record state to confirm dismissal."
                  : "Resubmitting below uses the identical body and submission key, so accepted work is not duplicated."}
              </Text>
            )}
          </Alert>
        )}
        {submission && action !== "dismiss" && (
          <Text size="xs" style={{ overflowWrap: "anywhere" }}>
            Submission key: {submission.key}
          </Text>
        )}
        {outcomes && (
          <Alert title="Action results" color="blue">
            <Stack gap="xs">
              {outcomes.map((outcome) => (
                <Text
                  key={outcome}
                  size="sm"
                  style={{ overflowWrap: "anywhere" }}
                >
                  {outcome}
                </Text>
              ))}
            </Stack>
          </Alert>
        )}
        <Group justify="flex-end">
          {!busy && (
            <Button variant="default" onClick={() => onClose(uncertain, !!outcomes)}>
              {outcomes
                ? "Done"
                : uncertain
                  ? "Close and inspect history"
                  : "Cancel"}
            </Button>
          )}
          {uncertain && (
            <Button
              variant="default"
              onClick={
                action === "dismiss" ? () => void checkDismissal() : onRefresh
              }
              loading={busy}
            >
              Check current state
            </Button>
          )}
          {!outcomes && (!uncertain || action !== "dismiss") && (
            <Button
              color={action === "dismiss" ? "red.9" : undefined}
              loading={busy}
              disabled={
                (needsAck && !ack) ||
                (action === "dismiss" && !confirmed) ||
                (action !== "dismiss" && !local && !apiValidation)
              }
              onClick={() => void submit()}
            >
              {uncertain
                ? "Resubmit same request"
                : action === "retry"
                  ? "Queue retry"
                  : action === "resolve"
                    ? "Record confirmation"
                    : "Dismiss and delete artifacts"}
            </Button>
          )}
        </Group>
      </Stack>
    </Modal>
  );
}
