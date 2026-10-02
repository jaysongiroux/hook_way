import { beforeEach, expect, test } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { MantineProvider } from "@mantine/core";
import { theme } from "../theme";
import { emptyDocument } from "./model";
import { useGraphStore } from "./store";
import {
  CredentialsPanel,
  changeCredentialKind,
  credentialKindSchema,
} from "./CredentialsPanel";
import { RoutingPanel, routingEntrySchema } from "./RoutingPanel";
import { credentialNameError, routingNameError } from "./WorkspaceEntries";
import { schemaProperties, schemaRequired } from "./nodeSchema";

beforeEach(() =>
  useGraphStore.setState({
    ...emptyDocument(),
    selectedId: null,
    selectedEdgeId: null,
  }),
);
const credentials = () =>
  useGraphStore.getState().configExtra.credentials as Record<
    string,
    Record<string, unknown>
  >;

test("workspace name contracts match routing and graph namespaces", () => {
  expect(routingNameError("prod-us")).toBeUndefined();
  expect(routingNameError("prod_us")).toBeDefined();
  expect(routingNameError("Prod")).toBeDefined();
  expect(credentialNameError("sample.profile")).toBeUndefined();
  expect(credentialNameError("sample>profile")).toBeDefined();
  expect(credentialNameError("sample profile")).toBeDefined();
});

test("credential strategies show generated fields with their conditional requirements", () => {
  expect(schemaRequired(credentialKindSchema("static"))).toEqual([
    "allowed_origins",
    "headers",
  ]);
  expect(
    schemaRequired(credentialKindSchema("oauth_client_credentials")),
  ).toContain("client_secret");
  expect(schemaProperties(credentialKindSchema("static"))).not.toHaveProperty(
    "token_url",
  );
  expect(
    schemaProperties(credentialKindSchema("oauth_client_credentials")),
  ).not.toHaveProperty("secret_parameters");
  expect(
    schemaProperties(credentialKindSchema("token_exchange")),
  ).not.toHaveProperty("ttl");
  expect(() => credentialKindSchema("constructor")).not.toThrow();
  expect(schemaRequired(routingEntrySchema("stores"))).toEqual(["table"]);
});

test("explicit credential strategy change prunes recognized incompatible options and retains unknown options", () => {
  const entry = {
    kind: "oauth_client_credentials",
    client_id: { provider: "env", name: "ID" },
    client_secret: { provider: "env", name: "SECRET" },
    token_url: "https://auth.example.com/token",
    ttl: "5m",
    allowed_origins: ["https://api.example.com"],
    future: { keep: true },
    headers: {
      Authorization: { secret_ref: { provider: "env", name: "OLD" } },
      "X-Extra": { secret_ref: { provider: "env", name: "EXTRA" } },
    },
  };
  const exchange = changeCredentialKind(entry, "token_exchange");
  expect(exchange).not.toHaveProperty("client_id");
  expect(exchange).not.toHaveProperty("ttl");
  expect(exchange.headers).toHaveProperty("X-Extra");
  expect(exchange.headers).not.toHaveProperty("Authorization");
  expect(exchange.future).toEqual({ keep: true });
  expect(changeCredentialKind(entry, "static")).not.toHaveProperty("token_url");
  expect(entry).toHaveProperty("client_id");
});

test("fresh workspace can create a credential profile and duplicate drafts cannot overwrite it", () => {
  render(
    <MantineProvider theme={theme}>
      <CredentialsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.change(screen.getByLabelText("New credential profile name"), {
    target: { value: "sample" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Add credential profile" }),
  );
  expect(credentials().sample).toEqual({ kind: "static", allowed_origins: [] });
  fireEvent.change(screen.getByLabelText("New credential profile name"), {
    target: { value: "sample" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Add credential profile" }),
  );
  expect(screen.getByText("That name already exists.")).toBeInTheDocument();
  expect(Object.keys(credentials())).toEqual(["sample"]);
  fireEvent.change(screen.getByLabelText("New managed header name"), {
    target: { value: "Authorization" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add managed header" }));
  expect(credentials().sample.headers).toEqual({
    Authorization: { secret_ref: { provider: "env", name: "" } },
  });
  expect(screen.getByText("Header secret reference")).toBeInTheDocument();
});

test("imported inactive credential settings survive ordinary edits until explicit removal", () => {
  useGraphStore.setState({
    configExtra: {
      extra: true,
      credentials: {
        sample: {
          kind: "static",
          allowed_origins: [],
          ttl: "3m",
          custom: "retained",
        },
      },
    },
  });
  render(
    <MantineProvider theme={theme}>
      <CredentialsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  expect(screen.getByText(/Imported settings inactive/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText("New managed header name"), {
    target: { value: "X-Key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add managed header" }));
  expect(credentials().sample).toHaveProperty("ttl", "3m");
  expect(credentials().sample).toHaveProperty("custom", "retained");
  fireEvent.click(
    screen.getByRole("button", { name: "Remove inactive settings" }),
  );
  expect(credentials().sample).not.toHaveProperty("ttl");
  expect(credentials().sample).toHaveProperty("custom", "retained");
  expect(useGraphStore.getState().configExtra.extra).toBe(true);
});

test("routing entries can be authored from an empty workspace without overwriting unrelated extra configuration", () => {
  useGraphStore.setState({ configExtra: { extra: { retained: true } } });
  render(
    <MantineProvider theme={theme}>
      <RoutingPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.change(screen.getByLabelText("New routing profile name"), {
    target: { value: "primary" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add routing profile" }));
  expect(useGraphStore.getState().configExtra.routing).toEqual({
    profiles: { primary: {} },
  });
  fireEvent.click(screen.getByRole("tab", { name: "Environments" }));
  const panel = screen.getByRole("tabpanel");
  fireEvent.change(within(panel).getByLabelText("New environment name"), {
    target: { value: "test" },
  });
  fireEvent.click(
    within(panel).getByRole("button", { name: "Add environment" }),
  );
  expect(useGraphStore.getState().configExtra.routing).toEqual({
    profiles: { primary: {} },
    environments: { test: { profiles: [], accounts: [] } },
  });
  expect(useGraphStore.getState().configExtra.extra).toEqual({
    retained: true,
  });
});

test("unsupported imported strategy stays visible and is not silently replaced", () => {
  useGraphStore.setState({
    configExtra: {
      credentials: {
        sample: {
          kind: "constructor",
          allowed_origins: ["https://api.example.com"],
          future: { keep: true },
        },
      },
    },
  });
  render(
    <MantineProvider theme={theme}>
      <CredentialsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  expect(
    screen.getByDisplayValue("Unsupported: constructor"),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/This imported strategy is unsupported/),
  ).toBeInTheDocument();
  expect(credentials().sample.kind).toBe("constructor");
  expect(credentials().sample.future).toEqual({ keep: true });
});

test("removing a credential profile also removes its unresolved map draft", () => {
  const name = "sample|scope/part";
  useGraphStore.setState({
    configExtra: {
      credentials: {
        [name]: {
          kind: "token_exchange",
          allowed_origins: [],
          token_url: "https://auth.example.com/token",
          token_path: "token",
          parameters: {},
        },
      },
    },
  });
  render(
    <MantineProvider theme={theme}>
      <CredentialsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add entry" }));
  const key = `credentials/${encodeURIComponent(name)}|credentials.${name}.parameters`;
  expect(useGraphStore.getState().formDrafts?.[key]?.error).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: `Remove credential profile ${name}` }),
  );
  expect(useGraphStore.getState().formDrafts).toEqual({});
  expect(credentials()).toEqual({});
});

test("renaming a credential profile preserves the visible invalid draft under its new owner", () => {
  const name = "sample|scope/part",
    renamed = "renamed|scope/part";
  useGraphStore.setState({
    configExtra: {
      credentials: {
        [name]: {
          kind: "token_exchange",
          allowed_origins: [],
          token_url: "https://auth.example.com/token",
          token_path: "token",
          parameters: {},
        },
      },
    },
  });
  render(
    <MantineProvider theme={theme}>
      <CredentialsPanel opened onClose={() => {}} />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Add entry" }));
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: renamed },
  });
  fireEvent.click(screen.getByRole("button", { name: "Rename" }));
  expect(
    useGraphStore.getState().formDrafts?.[
      `credentials/${encodeURIComponent(renamed)}|credentials.${renamed}.parameters`
    ]?.error,
  ).toBeTruthy();
  expect(
    useGraphStore.getState().formDrafts?.[
      `credentials/${encodeURIComponent(name)}|credentials.${name}.parameters`
    ],
  ).toBeUndefined();
  expect(screen.getByText("Every entry needs a key.")).toBeInTheDocument();
});
