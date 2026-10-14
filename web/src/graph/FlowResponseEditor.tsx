import { useEffect, useRef, useState } from "react";
import { Textarea } from "@mantine/core";
import { useGraphStore } from "./store";

export function FlowResponseEditor() {
  const activeFlow = useGraphStore((s) => s.activeFlow);
  const responses = useGraphStore((s) => s.flowExtra?.responses);
  const setFlowResponses = useGraphStore((s) => s.setFlowResponses);
  const [text, setText] = useState("");
  const [error, setError] = useState<string | undefined>();
  const lastWrite = useRef<unknown>(responses);
  const lastFlow = useRef<string | undefined>(undefined);
  const write = (value: unknown) => {
    lastWrite.current = value;
    setFlowResponses(value);
  };
  useEffect(() => {
    if (lastFlow.current === activeFlow && lastWrite.current === responses)
      return;
    lastFlow.current = activeFlow;
    lastWrite.current = responses;
    setText(
      responses === undefined
        ? ""
        : typeof responses === "string"
          ? responses
          : JSON.stringify(responses, null, 2),
    );
    setError(
      typeof responses === "string"
        ? "Enter a JSON object with response settings."
        : undefined,
    );
  }, [activeFlow, responses]);
  return (
    <Textarea
      label="HTTP response policy"
      description='Optional JSON object keyed by outcome: responded, published, dropped, publish_error, partial_failure, auth_error, body_error, lookup_error, payload_too_large or error. Each entry has status, optional body and headers. Example: {"published":{"status":200,"body":"ok"}}. Returning 2xx for an error tells the sender the request succeeded and can stop its retries. Delivery metrics retain the actual outcome.'
      autosize
      minRows={3}
      maxRows={18}
      value={text}
      error={error}
      onChange={(event) => {
        const next = event.currentTarget.value;
        setText(next);
        if (!next.trim()) {
          setError(undefined);
          write(undefined);
          return;
        }
        try {
          const value: unknown = JSON.parse(next);
          if (!value || typeof value !== "object" || Array.isArray(value))
            throw new Error("object required");
          setError(undefined);
          write(value);
        } catch {
          setError(
            "Enter a JSON object with response settings. Invalid edits are retained until corrected.",
          );
          // Keep invalid edits in the document so validation/export cannot
          // silently revert to a previously saved response policy.
          write(next);
        }
      }}
    />
  );
}
