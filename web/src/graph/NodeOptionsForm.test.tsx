import { useState } from "react";
import { beforeEach, expect, test, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { NodeOptionsForm } from "./NodeOptionsForm";
import { defaultNodeOptions, NODE_SCHEMAS, NODE_TYPES } from "./nodeSchema";
import { useGraphStore } from "./store";

beforeEach(() => {
  useGraphStore.getState().clear();
  useGraphStore.getState().setDestinations({
    standard: { type: "sqs" },
    topic: { type: "sns" },
    objects: { type: "s3" },
    callback: { type: "webhook" },
  });
  useGraphStore.getState().setConfigExtra({
    credentials: {
      service: { type: "bearer", secret: "${TOKEN}" },
      alternate: { type: "bearer", secret: "${OTHER}" },
    },
    routing: {
      profiles: {
        sandbox: { account_id: "000000000000", credential_mode: "sandbox" },
      },
      stores: { mappings: { table: "routes" } },
      environments: {
        preview: {
          profiles: ["sandbox"],
          accounts: ["000000000000"],
          destination_prefix: "preview",
        },
      },
    },
  });
});

function setup(nodeType: string, initial: Record<string, unknown>) {
  const change = vi.fn();
  function Controlled() {
    const [value, setValue] = useState(initial);
    return (
      <MantineProvider>
        <NodeOptionsForm
          nodeType={nodeType}
          value={value}
          onChange={(next) => {
            change(next);
            setValue(next);
          }}
          nodeId="example"
          scope={`node-form-test/${nodeType}`}
        />
      </MantineProvider>
    );
  }
  const rendered = render(<Controlled />);
  return { change, ...rendered };
}
function choose(label: string, option: string, container?: HTMLElement) {
  const query = container ? within(container) : screen;
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  fireEvent.click(
    query.getByRole("textbox", { name: new RegExp(`^${escaped}\\s*\\*?$`) }),
  );
  fireEvent.click(screen.getByRole("option", { name: option }));
}
const auth = {
  scheme: "hmac_sha256_hex",
  secret: "${SECRET}",
  signature_header: "X-Signature",
};

for (const [scheme, capability] of Object.entries(
  NODE_SCHEMAS.auth.auth_schemes ?? {},
)) {
  test(`registered auth strategy ${scheme} exposes its active requirements without mutating on open`, () => {
    const { change } = setup("auth", { ...auth, scheme });
    expect(change).not.toHaveBeenCalled();
    expect(
      screen.getByRole("textbox", { name: /Authentication strategy/ }),
    ).toBeInTheDocument();
    if (capability.jwt)
      expect(screen.getByLabelText("Raw body SHA-256 claim")).toBeRequired();
    else
      expect(
        screen.queryByLabelText("Raw body SHA-256 claim"),
      ).not.toBeInTheDocument();
    if (capability.request_signature)
      expect(
        screen.getByRole("textbox", { name: "timestamp unit" }),
      ).toBeRequired();
    else
      expect(
        screen.queryByRole("textbox", { name: "timestamp unit" }),
      ).not.toBeInTheDocument();
    if (
      capability.jwt ||
      capability.request_signature ||
      ["hmac_sha256_ts_colon", "hmac_sha256_ts_dot"].includes(scheme)
    )
      expect(
        screen.getByLabelText(
          capability.jwt ? "JWT clock tolerance" : "timestamp tolerance",
        ),
      ).toBeInTheDocument();
    if (capability.separate_timestamp)
      expect(screen.getByLabelText("timestamp header")).toBeRequired();
    else
      expect(
        screen.queryByLabelText("timestamp header"),
      ).not.toBeInTheDocument();
    if (capability.key_format === "pem") {
      expect(screen.getByLabelText("Verification key").tagName).toBe(
        "TEXTAREA",
      );
      expect(screen.getByText(/Use a PEM public key/)).toBeInTheDocument();
    }
    if (capability.key_format === "base64_der")
      expect(
        screen.getByText(/Base64-encoded PKIX DER P-256/),
      ).toBeInTheDocument();
    if (scheme === "basic")
      expect(screen.getByText(/username:password/)).toBeInTheDocument();
  });
}

test("auth strategy switching removes inactive JWT fields and keeps unrelated authored values", () => {
  const { change } = setup("auth", {
    scheme: "jwt_hs256",
    secret: "${SECRET}",
    signature_header: "Authorization",
    body_hash_claim: "body_hash",
    expected_issuer: "issuer",
    custom: "retained",
  });
  choose("Authentication strategy", "HMAC SHA-256 · hexadecimal");
  expect(change).toHaveBeenLastCalledWith({
    scheme: "hmac_sha256_hex",
    secret: "${SECRET}",
    signature_header: "Authorization",
    custom: "retained",
  });
  choose("Authentication strategy", "JWT · HS256");
  expect(screen.getByLabelText("Raw body SHA-256 claim")).toBeRequired();
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("body_hash_claim");
});

test("auth credential header/query changes serialize one effective source", () => {
  const { change } = setup("auth", auth);
  choose("Credential location", "Query parameter");
  expect(change).toHaveBeenLastCalledWith({
    scheme: "hmac_sha256_hex",
    secret: "${SECRET}",
    source: { kind: "query", key: "X-Signature" },
  });
  expect(
    screen.queryByLabelText("Signature header name"),
  ).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("Query parameter name"), {
    target: { value: "signature" },
  });
  choose("Credential location", "Request header");
  expect(change).toHaveBeenLastCalledWith({
    scheme: "hmac_sha256_hex",
    secret: "${SECRET}",
    source: { kind: "header", key: "signature" },
  });
});

test("auth key source changes remove the mutually exclusive inactive credential", () => {
  const { change } = setup("auth", auth);
  choose("Key source", "Secret reference (recommended)");
  expect(change).toHaveBeenLastCalledWith({
    scheme: auth.scheme,
    signature_header: auth.signature_header,
    secret_ref: { provider: "env", name: "" },
  });
  choose("Key source", "Inline key / environment interpolation");
  expect(change).toHaveBeenLastCalledWith({
    scheme: auth.scheme,
    signature_header: auth.signature_header,
    secret: "",
  });
  expect(screen.getByLabelText("Verification key")).toHaveAttribute(
    "type",
    "password",
  );
});

test("auth inactive imports remain with an explicit removal path", () => {
  const { change } = setup("auth", {
    ...auth,
    body_hash_claim: "old_claim",
    expected_audience: "old",
  });
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByText(
      /These settings do not apply to this mode: body_hash_claim, expected_audience|These settings do not apply to this mode: expected_audience, body_hash_claim/,
    ),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove inactive settings" }),
  );
  expect(change).toHaveBeenLastCalledWith(auth);
});

test("standard SQS exposes standard options and FIFO switching removes per-message delay", () => {
  const { change } = setup("publish", {
    destination: "standard",
    target: "events",
    create: false,
    sqs: { delay_seconds: 5, queue_owner_account_id: "111111111111" },
  });
  expect(screen.getByLabelText("Queue name")).toHaveValue("events");
  choose("Queue / topic behavior", "FIFO");
  expect(change.mock.calls.at(-1)?.[0]).toMatchObject({
    target: "events.fifo",
    create: false,
    sqs: { queue_owner_account_id: "111111111111" },
    fifo: { content_based_deduplication: true },
  });
  expect(change.mock.calls.at(-1)?.[0].sqs).not.toHaveProperty("delay_seconds");
});

test("SNS FIFO switching removes standard-only subject but keeps existing topic binding", () => {
  const { change } = setup("publish", {
    destination: "topic",
    target: "events",
    create: false,
    sns: {
      subject: "Notify",
      topic_arn: "arn:aws:sns:us-east-1:111111111111:events",
    },
  });
  expect(screen.getByLabelText("Topic name")).toHaveValue("events");
  choose("Queue / topic behavior", "FIFO");
  expect(change.mock.calls.at(-1)?.[0].sns).not.toHaveProperty("subject");
});

test("publish deduplication and identity source choices serialize only the active contract", () => {
  const { change } = setup("publish", {
    destination: "standard",
    target: "events.fifo",
    fifo: {
      group_id: { source: "static", value: "group" },
      content_based_deduplication: true,
    },
  });
  choose("Deduplication", "Explicit deduplication identity");
  expect(change.mock.calls.at(-1)?.[0].fifo).toEqual({
    group_id: { source: "static", value: "group" },
    deduplication_id: { source: "raw_sha256" },
  });
  const sourceInputs = screen.getAllByLabelText("source");
  fireEvent.click(sourceInputs[0]);
  fireEvent.click(screen.getByRole("option", { name: "body" }));
  expect(change.mock.calls.at(-1)?.[0].fifo.group_id).toEqual({
    source: "body",
  });
  expect(screen.getByLabelText("path")).toBeRequired();
  choose("Deduplication", "Content-based deduplication");
  expect(change.mock.calls.at(-1)?.[0].fifo).not.toHaveProperty(
    "deduplication_id",
  );
});

test("changing a broker destination to object storage removes broker-only settings", () => {
  const { change } = setup("publish", {
    destination: "standard",
    target: "events",
    sqs: { delay_seconds: 2 },
    retry: { max_attempts: 2 },
    wire_body: "raw",
    fifo: { group_id: { source: "static", value: "x" } },
    create: false,
  });
  choose("Destination", "objects");
  expect(change).toHaveBeenLastCalledWith({
    destination: "objects",
    target: "events",
    create: false,
  });
  expect(screen.getByLabelText("Payload to send")).toBeInTheDocument();
  expect(
    screen.queryByLabelText("Queue / topic behavior"),
  ).not.toBeInTheDocument();
});

test("HTTP URL/upstream and fixed/forward method switches clean inactive request fields", () => {
  const { change } = setup("http_request", {
    url: "https://example.test",
    method: "PATCH",
    body: "raw",
    timeout: "5s",
  });
  choose("Target mode", "Backend selected by routing rules");
  expect(change).toHaveBeenLastCalledWith({
    method: "PATCH",
    body: "raw",
    timeout: "5s",
    upstream: { profile: "", environment_variable: "route" },
  });
  choose("HTTP method mode", "Forward incoming method");
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("method");
  expect(change.mock.calls.at(-1)?.[0]).toHaveProperty("forward_method", true);
  choose("Target mode", "URL or URL template");
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("upstream");
});

test("managed HTTP credentials exclude retry and redirect configuration on enable", () => {
  const { change } = setup("http_request", {
    url: "https://example.test",
    retry: { max_attempts: 2 },
    redirects: { max_hops: 2 },
  });
  fireEvent.click(screen.getByLabelText("Use managed credentials"));
  expect(change).toHaveBeenLastCalledWith({
    url: "https://example.test",
    credential: { profile: "" },
  });
});

test("credential fixed/environment modes replace conflicting selection fields", () => {
  const { change } = setup("http_request", {
    url: "https://example.test",
    credential: { profile: "service" },
  });
  choose("Credential selection", "Credential profile per allowed environment");
  expect(change.mock.calls.at(-1)?.[0].credential).toEqual({
    routing_profile: "",
    environment_variable: "route",
    environment_profiles: {},
  });
  choose("Credential routing profile", "sandbox");
  choose("Credential for preview", "alternate");
  expect(change.mock.calls.at(-1)?.[0].credential.environment_profiles).toEqual(
    { preview: "alternate" },
  );
  choose("Credential selection", "One credential profile");
  expect(change.mock.calls.at(-1)?.[0].credential).toEqual({ profile: "" });
});

test("routing fixed/request/store modes produce exclusive export shapes", () => {
  const { change } = setup("route", {
    profile: "sandbox",
    fixed: "preview",
    name: "route",
  });
  choose("Routing mode", "Lookup identifier in a routing store");
  expect(change).toHaveBeenLastCalledWith({
    profile: "sandbox",
    name: "route",
    store: "",
    candidates: [{ source: "body", path: "", id_type: "" }],
  });
  choose("Routing mode", "Environment from request");
  expect(change).toHaveBeenLastCalledWith({
    profile: "sandbox",
    name: "route",
    candidates: [{ source: "body", path: "" }],
  });
  choose("Routing mode", "Always use one allowed environment");
  expect(change).toHaveBeenLastCalledWith({
    profile: "sandbox",
    name: "route",
    fixed: "",
  });
});

test("registration after-create mode removes direct-only headers and selects upstream identifiers", () => {
  const { change } = setup("register_route", {
    mode: "direct",
    profile: "sandbox",
    store: "mappings",
    environment_variable: "route",
    id_source: "body",
    id_path: "id",
    id_type: "event",
    require_headers: ["X-ID"],
  });
  choose("Registration mode", "After upstream creation");
  expect(change.mock.calls.at(-1)?.[0]).toMatchObject({
    mode: "after_create",
    id_source: "upstream",
  });
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("require_headers");
  expect(
    screen.getByText(/preceding Call an HTTP API node/),
  ).toBeInTheDocument();
});

test("response fixed/upstream switches preserve common headers and clear inactive response fields", () => {
  const { change } = setup("respond", {
    status: 200,
    body: "ok",
    headers: { "X-Trace": "present" },
  });
  choose("Response mode", "Return upstream response");
  expect(change).toHaveBeenLastCalledWith({
    passthrough: true,
    headers: { "X-Trace": "present" },
  });
  choose("Response mode", "Fixed response");
  expect(change).toHaveBeenLastCalledWith({
    status: 200,
    headers: { "X-Trace": "present" },
  });
});

for (const type of NODE_TYPES) {
  test(`catalog node ${type} opens its form without mutating configuration`, () => {
    const { change, container } = setup(
      type,
      defaultNodeOptions(type, "example"),
    );
    expect(container.firstChild).toBeInTheDocument();
    expect(change).not.toHaveBeenCalled();
  });
}

test("auth strategy picker exposes the entire generated registry and excludes the separate no-auth node", () => {
  setup("auth", auth);
  fireEvent.click(
    screen.getByRole("textbox", { name: "Authentication strategy" }),
  );
  const options = screen.getAllByRole("option");
  expect(options.map((option) => option.getAttribute("value")).sort()).toEqual(
    Object.keys(NODE_SCHEMAS.auth.auth_schemes ?? {}).sort(),
  );
  expect(options.map((option) => option.getAttribute("value"))).not.toContain(
    "no_auth",
  );
});

test("unsupported imported credential locations remain visible and retained until changed", () => {
  const imported = {
    scheme: auth.scheme,
    secret: auth.secret,
    source: { kind: "cookie", key: "session" },
  };
  const { change } = setup("auth", imported);
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByText(/Imported credential source cookie is unsupported/),
  ).toBeInTheDocument();
  choose("Credential location", "Query parameter");
  expect(change).toHaveBeenLastCalledWith({
    ...imported,
    source: { kind: "query", key: "session" },
  });
});

test("imported FIFO per-message delay has a direct removal path that preserves the existing binding", () => {
  const { change } = setup("publish", {
    destination: "standard",
    target: "events.fifo",
    fifo: {
      group_id: { source: "static", value: "group" },
      content_based_deduplication: true,
    },
    sqs: { delay_seconds: 5, queue_owner_account_id: "111111111111" },
  });
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByText(/These settings do not apply to this mode: delay_seconds/),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove inactive settings" }),
  );
  expect(change.mock.calls.at(-1)?.[0].sqs).toEqual({
    queue_owner_account_id: "111111111111",
  });
});

test("imported FIFO SNS subject has a direct removal path that preserves the existing ARN", () => {
  const topic_arn = "arn:aws:sns:us-east-1:111111111111:events.fifo";
  const { change } = setup("publish", {
    destination: "topic",
    target: "events.fifo",
    fifo: {
      group_id: { source: "static", value: "group" },
      content_based_deduplication: true,
    },
    sns: { subject: "Notify", topic_arn },
  });
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByText(/These settings do not apply to this mode: subject/),
  ).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove inactive settings" }),
  );
  expect(change.mock.calls.at(-1)?.[0].sns).toEqual({ topic_arn });
});

test("unknown imported auth strategies show an error and are never replaced merely by opening the form", () => {
  const { change } = setup("auth", { ...auth, scheme: "future_auth_strategy" });
  expect(
    screen.getByText("Unsupported strategy. Choose a supported strategy."),
  ).toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
  expect(
    screen.getByRole("textbox", { name: "Authentication strategy" }),
  ).toHaveAttribute("aria-invalid", "true");
});

test("auth header names with an imported structured value show an inline type error without auto-writing", () => {
  const { change } = setup("auth", {
    scheme: auth.scheme,
    secret: auth.secret,
    source: { kind: "header", key: { imported: "invalid" } },
  });
  expect(change).not.toHaveBeenCalled();
  expect(screen.getByLabelText("Signature header name")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
});

test("HTTP credential environment mappings use canonical names and allow partial mappings", () => {
  const state = useGraphStore.getState();
  const routing = state.configExtra.routing as Record<string, unknown>;
  state.setConfigExtra({
    ...state.configExtra,
    routing: {
      ...routing,
      environments: {
        preview: {
          profiles: ["sandbox"],
          accounts: ["000000000000"],
          aliases: ["pre"],
        },
        live: { profiles: ["sandbox"], accounts: ["000000000000"] },
      },
    },
  });
  const { change } = setup("http_request", {
    url: "https://example.test",
    credential: {
      routing_profile: "sandbox",
      environment_variable: "route",
      environment_profiles: { preview: "service", pre: "alternate" },
    },
  });
  expect(
    screen.getByRole("textbox", { name: "Credential for preview" }),
  ).not.toBeRequired();
  expect(
    screen.getByRole("textbox", { name: "Credential for live" }),
  ).not.toBeRequired();
  expect(
    screen.queryByRole("textbox", { name: "Credential for pre" }),
  ).not.toBeInTheDocument();
  expect(change).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Remove mapping pre" }));
  expect(change.mock.calls.at(-1)?.[0].credential.environment_profiles).toEqual(
    { preview: "service" },
  );
});
test("webhook publication describes an appended target path", () => {
  setup("publish", { destination: "callback", target: "events" });
  expect(
    screen.getByRole("textbox", { name: /Target path/ }),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/path is appended to the destination base URL/),
  ).toBeInTheDocument();
});

test("Temporal settings are editable and switching destinations removes only incompatible options", () => {
  useGraphStore
    .getState()
    .setDestinations({
      ...useGraphStore.getState().destinations,
      workflows: {
        type: "temporal",
        temporal: { host_port: "localhost:7233" },
      },
    });
  const { change } = setup("publish", {
    destination: "workflows",
    target: "orders",
    temporal: {
      workflow_type: "OrderWorkflow",
      workflow_id: { source: "body", path: "id" },
    },
  });
  expect(screen.getByLabelText("Task queue")).toHaveValue("orders");
  fireEvent.change(screen.getByLabelText("Workflow type"), {
    target: { value: "InvoiceWorkflow" },
  });
  expect(change.mock.calls.at(-1)?.[0].temporal.workflow_type).toBe(
    "InvoiceWorkflow",
  );
  fireEvent.click(screen.getByRole("textbox", { name: "Workflow ID source" }));
  fireEvent.click(screen.getByRole("option", { name: "Request header" }));
  expect(change.mock.calls.at(-1)?.[0].temporal.workflow_id).toEqual({
    source: "header",
  });
  fireEvent.change(screen.getByLabelText("Event ID header"), {
    target: { value: "X-Event-ID" },
  });
  expect(change.mock.calls.at(-1)?.[0].temporal.workflow_id).toEqual({
    source: "header",
    path: "X-Event-ID",
  });
  fireEvent.click(screen.getByLabelText("Destination"));
  fireEvent.click(screen.getByRole("option", { name: "standard" }));
  expect(change.mock.calls.at(-1)?.[0]).not.toHaveProperty("temporal");
  expect(change.mock.calls.at(-1)?.[0].target).toBe("orders");
});
