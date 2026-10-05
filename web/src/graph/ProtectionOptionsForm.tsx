import { Alert, Select, Stack, TextInput } from "@mantine/core";
import { useContext } from "react";
import { ReferenceField } from "../shared/ui/ReferenceField";
import { changeMode, Fields, InactiveSettings, WorkspaceActions, type OptionsProps } from "./FormPresentation";
import { useGraphStore } from "./store";
import { goDurationIssue } from "./goDuration";
import { record } from "./WorkspaceEntries";

export function ProtectionOptionsForm(props: OptionsProps) {
  const { nodeType, value, onChange, scope } = props;
  const open = useContext(WorkspaceActions);
  const stores = useGraphStore((s) => record(s.configExtra.state_stores));
  const selected = record(stores[String(value.store ?? "")]);
  const rate = nodeType === "rate_limit";
  const strategy = String(value.strategy ?? "token_bucket");
  const stage = String(value.stage ?? "request_head");
  const key = String(value.key_by ?? "source_ip");
  const header = key.startsWith("header.");
  const identities = stage === "verified" ? [{ value: "credential_id", label: "Verified credential ID" }, { value: "jwt_subject", label: "Verified JWT subject" }, { value: "jwt_tenant", label: "Verified JWT tenant claim" }] : [{ value: "source_ip", label: "Source IP" }, { value: "header", label: "Request header" }];
  const identity = header ? "header" : key;
  const supportedIdentity = identities.some((option) => option.value === identity);
  const incompatible = rate
    ? (strategy === "sliding_window_counter" ? ["rpm", "burst"] : ["limit", "window"]).filter((field) => value[field] !== undefined)
    : [];
  return (
    <Stack gap="sm">
      <ReferenceField label="Protection store" value={value.store} names={Object.keys(stores)}
        onChange={(store) => onChange({ ...value, store })} description="Required. Choose where this policy keeps its quota or duplicate reservations."
        onManage={() => open("state_stores")} manageLabel="Manage protection stores" />
      {value.store !== undefined && Object.keys(selected).length > 0 && (
        <Alert color="blue">{selected.memory !== undefined
          ? "This memory store protects one process. Quotas and duplicate reservations reset on restart."
          : "This shared store coordinates instances using the same deployment namespace and policy. Store failures reject protected requests with 503."}</Alert>
      )}
      <Fields {...props} fields={["policy_id"]} labels={{ policy_id: "Shared policy ID" }}
        fieldHelp={() => "Optional. Omitted isolates this flow and node. Use the same ID only when endpoints intentionally share the same policy and identity rules. Changes to an existing policy use an explicit coordinated update."} />
      {rate ? (
        <>
          <Select label="Rate-limiting strategy" value={strategy} allowDeselect={false}
            data={[{ value: "token_bucket", label: "Token bucket · steady refill with bursts" }, { value: "sliding_window_counter", label: "Sliding-window counter · approximate rolling quota" }, ...(!["token_bucket", "sliding_window_counter"].includes(strategy) ? [{ value: strategy, label: strategy }] : [])]}
            error={!["token_bucket", "sliding_window_counter"].includes(strategy) ? "Choose a supported strategy." : undefined}
            onChange={(next) => next && changeMode(value, ["rpm", "burst", "limit", "window"], next === "sliding_window_counter" ? { strategy: next, limit: 100, window: "1m" } : { strategy: next, rpm: 60 }, onChange, scope)} />
          {strategy === "sliding_window_counter" && <Alert color="blue">Uses a weighted estimate of adjacent windows. This is an approximate rolling quota, rather than a strict ceiling in every rolling interval.</Alert>}
          <Fields {...props} fields={strategy === "sliding_window_counter" ? ["limit", "window", "max_keys"] : ["rpm", "burst", "max_keys"]}
            required={strategy === "sliding_window_counter" ? ["limit", "window"] : ["rpm"]}
            errors={{window: goDurationIssue(value.window) ?? ""}}
            labels={{ rpm: "Requests per minute", burst: "Burst allowance", limit: "Requests per window", window: "Window duration", max_keys: "Maximum tracked identities" }}
            fieldHelp={(path) => ({ rpm: "Tokens refill continuously at this rate. Reaching this node consumes one token, even if processing later fails.", burst: "Maximum accumulated tokens and immediate burst. Omitted uses requests per minute.", limit: "Approximate maximum admitted requests in the rolling window.", window: "Rolling quota duration, such as 1m or 1h.", max_keys: "Live identities tracked by this policy. At capacity, new identities receive 503; existing identities keep their protection. Omitted uses 10,000." } as Record<string, string>)[path]} />
          <Select label="Identity stage" value={stage} allowDeselect={false}
            data={[{ value: "request_head", label: "Before body/authentication · IP or header" }, { value: "verified", label: "After authentication · verified identity" }, ...(!["request_head", "verified"].includes(stage) ? [{ value: stage, label: stage }] : [])]}
            error={!["request_head", "verified"].includes(stage) ? "Choose a supported stage." : undefined}
            description="Changing stage changes the node's input and output. Reconnect its edges to the matching workflow stage."
            onChange={(next) => next && changeMode(value, ["key_by"], { stage: next, key_by: next === "verified" ? "credential_id" : "source_ip" }, onChange, scope)} />
          <Select label="Rate-limit identity" allowDeselect={false} value={header ? "header" : key}
            data={[...identities, ...(!supportedIdentity ? [{value: identity, label: identity}] : [])]}
            error={!supportedIdentity ? "Choose a supported identity for this stage." : undefined}
            onChange={(next) => next && onChange({ ...value, key_by: next === "header" ? "header." : next })} />
          {header && <TextInput label="Rate-limit header" aria-label="Rate-limit header" required value={key.slice(7)}
            description="A missing header returns 400. Use a trusted upstream to set customer identity headers; callers can otherwise change them to obtain fresh quota."
            error={!key.slice(7) ? "Enter the header name." : undefined}
            onChange={(event) => onChange({ ...value, key_by: `header.${event.currentTarget.value}` })} />}
          {stage === "verified" && <Alert color="blue">Connect after Verify signature or token. Configure a stable credential ID there; JWT identities also require an expected issuer and audience. Tenant quotas use the configured tenant claim. Public access cannot supply a verified identity.</Alert>}
          <InactiveSettings value={value} fields={incompatible} onRemove={() => changeMode(value, incompatible, {}, onChange, scope)} />
        </>
      ) : (
        <>
          <Fields {...props} fields={["ttl", "key", "max_entries"]}
            errors={{ttl: goDurationIssue(value.ttl) ?? ""}}
            labels={{ ttl: "Duplicate window", key: "Duplicate identity", max_entries: "Maximum live reservations" }}
            fieldHelp={(path) => ({ ttl: "Protection starts when this request is admitted. Omitted uses 5m. Duplicates do not extend the window; delayed cleanup does not extend it either.", key: "Optional template using request headers or path values. Prefer a stable provider event ID. Omitted hashes the original body; identical bodies then share protection.", max_entries: "Live reservations per policy. At capacity, new events receive 503; unexpired protection is never evicted. Omitted uses 1,024." } as Record<string, string>)[path]} />
          <Alert color="blue">Protection remains after partial or uncertain delivery. It is released only when no delivery side effect occurred, using the reservation owner. Recover retained failures with destination-only replay.</Alert>
        </>
      )}
    </Stack>
  );
}
