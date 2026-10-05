import { Alert, Text } from "@mantine/core";
import { DARK } from "../tokens";

/** Explain the acknowledgement boundary wherever an HTTP destination is chosen. */
export function WebhookDeliveryNotice({
  deliveryMode,
}: {
  deliveryMode?: unknown;
}) {
  if (deliveryMode === "sync")
    return (
      <Text size="sm" c="dimmed">
        Publication waits for the HTTP destination response. A caller retry can
        repeat an already completed delivery; deduplicate with the sender's
        event ID.
      </Text>
    );
  if (deliveryMode != null && deliveryMode !== "" && deliveryMode !== "async")
    return null;
  return (
    <Alert
      color="orange"
      title="Queued in memory"
      styles={{ title: { color: DARK.text } }}
    >
      Async delivery acknowledges the job before the HTTP destination receives
      it. A crash or forced shutdown can lose queued and in-flight jobs, even
      with replay or a dead-letter destination. Choose sync to wait for
      delivery, or publish to a durable broker before acknowledging the sender.
    </Alert>
  );
}
