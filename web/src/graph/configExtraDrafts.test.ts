import { beforeEach, expect, test } from "vitest";
import { useGraphStore } from "./store";
import { emptyDocument } from "./model";

const draft = { text: "{", error: "Invalid JSON draft" };
beforeEach(() =>
  useGraphStore.setState({
    ...emptyDocument(),
    selectedId: null,
    selectedEdgeId: null,
  }),
);

function seed(
  credentials: Record<string, unknown>,
  drafts: Record<string, typeof draft>,
) {
  useGraphStore.setState({
    configExtra: { credentials, untouched: true },
    formDrafts: drafts,
  });
}

test("removing a credential profile clears its drafts and keeps unrelated owners", () => {
  seed(
    {
      first: { kind: "token_exchange", parameters: {} },
      second: { kind: "static" },
    },
    {
      "credentials/first|credentials.first.parameters": draft,
      "credentials/first/headers/X-Key/secret_ref|selection": draft,
      "credentials/second|credentials.second.parameters": draft,
      "node/sample/code|script": draft,
    },
  );
  useGraphStore
    .getState()
    .setConfigExtra({ credentials: { second: { kind: "static" } } });
  expect(Object.keys(useGraphStore.getState().formDrafts ?? {})).toEqual([
    "credentials/second|credentials.second.parameters",
    "node/sample/code|script",
  ]);
  expect(useGraphStore.getState().configExtra.untouched).toBe(true);
});

test("renaming a profile migrates both encoded scope and its full field paths", () => {
  const entry = { kind: "token_exchange", parameters: {} };
  const oldName = "first|scope/part",
    nextName = "next|scope/part";
  seed(
    { [oldName]: entry },
    {
      [`credentials/${encodeURIComponent(oldName)}|credentials.${oldName}.parameters`]:
        draft,
      [`credentials/${encodeURIComponent(oldName)}/headers/X-Key/secret_ref|selection`]:
        draft,
    },
  );
  useGraphStore
    .getState()
    .setConfigExtra({ credentials: { [nextName]: entry } });
  expect(useGraphStore.getState().formDrafts).toEqual({
    [`credentials/${encodeURIComponent(nextName)}|credentials.${nextName}.parameters`]:
      draft,
    [`credentials/${encodeURIComponent(nextName)}/headers/X-Key/secret_ref|selection`]:
      draft,
  });
});

test("ordinary credential edits retain unresolved drafts", () => {
  seed(
    {
      first: {
        kind: "token_exchange",
        parameters: {},
        token_url: "https://auth.example.com/token",
      },
    },
    { "credentials/first|credentials.first.parameters": draft },
  );
  useGraphStore.getState().setConfigExtra({
    credentials: {
      first: {
        kind: "token_exchange",
        parameters: {},
        token_url: "https://auth.example.com/next",
      },
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "credentials/first|credentials.first.parameters": draft,
  });
});

test("strategy changes prune drafts only for explicitly removed fields", () => {
  seed(
    { first: { kind: "token_exchange", parameters: {}, allowed_origins: [] } },
    {
      "credentials/first|credentials.first.parameters": draft,
      "credentials/first|credentials.first.allowed_origins": draft,
    },
  );
  useGraphStore.getState().setConfigExtra({
    credentials: { first: { kind: "static", allowed_origins: [] } },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "credentials/first|credentials.first.allowed_origins": draft,
  });
});

test("header and secret parameter renames/removals migrate or clear nested scopes", () => {
  const header = {
    secret_ref: {
      provider: "aws_secrets_manager",
      name: "sample",
      selection: "exact",
    },
  };
  const parameter = { provider: "env", name: "TOKEN" };
  seed(
    {
      first: {
        kind: "token_exchange",
        headers: { "X|Old": header },
        secret_parameters: { "old/parameter": parameter },
      },
    },
    {
      "credentials/first/headers/X%7COld/secret_ref|selection": draft,
      "credentials/first/secret_parameters/old%2Fparameter|name": draft,
    },
  );
  useGraphStore.getState().setConfigExtra({
    credentials: {
      first: {
        kind: "token_exchange",
        headers: { "X|New": header },
        secret_parameters: { "new/parameter": parameter },
      },
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "credentials/first/headers/X%7CNew/secret_ref|selection": draft,
    "credentials/first/secret_parameters/new%2Fparameter|name": draft,
  });
  useGraphStore.getState().setConfigExtra({
    credentials: {
      first: { kind: "token_exchange", headers: {}, secret_parameters: {} },
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({});
});

test("secret-provider changes clear removed fields in separately scoped reference forms", () => {
  seed(
    {
      first: {
        kind: "oauth_client_credentials",
        client_id: {
          provider: "aws_secrets_manager",
          name: "ID",
          selection: "exact",
        },
        headers: {
          "X-Key": {
            secret_ref: {
              provider: "aws_secrets_manager",
              name: "KEY",
              selection: "exact",
            },
          },
        },
      },
    },
    {
      "credentials/first/client_id|selection": draft,
      "credentials/first/headers/X-Key/secret_ref|selection": draft,
      "credentials/first/headers/X-Key/secret_ref|name": draft,
    },
  );
  useGraphStore.getState().setConfigExtra({
    credentials: {
      first: {
        kind: "oauth_client_credentials",
        client_id: { provider: "env", name: "ID" },
        headers: {
          "X-Key": { secret_ref: { provider: "env", name: "KEY" } },
        },
      },
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "credentials/first/headers/X-Key/secret_ref|name": draft,
  });
});

test("optional block child removal clears drafts using the parent's full path", () => {
  seed(
    {
      first: {
        kind: "oauth_client_credentials",
        invalid_session: { path: "error", value: "expired" },
      },
    },
    { "credentials/first|credentials.first.invalid_session.value": draft },
  );
  useGraphStore.getState().setConfigExtra({
    credentials: {
      first: {
        kind: "oauth_client_credentials",
        invalid_session: { path: "error" },
      },
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({});
});

test("routing category renames migrate paths and removals clear their drafts", () => {
  const profile = { account_id: "123456789012" },
    environment = { profiles: ["primary"] },
    store = { table: "sample" };
  useGraphStore.setState({
    configExtra: {
      routing: {
        profiles: { primary: profile },
        environments: { test: environment },
        stores: { registry: store },
      },
    },
    formDrafts: {
      "routing/profiles/primary|routing.profiles.primary.credential_mode":
        draft,
      "routing/environments/test|routing.environments.test.accounts": draft,
      "routing/stores/registry|routing.stores.registry.table": draft,
    },
  });
  useGraphStore.getState().setConfigExtra({
    routing: {
      profiles: { secondary: profile },
      environments: { test: environment },
      stores: {},
    },
  });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "routing/profiles/secondary|routing.profiles.secondary.credential_mode":
      draft,
    "routing/environments/test|routing.environments.test.accounts": draft,
  });
  useGraphStore.getState().setConfigExtra({ routing: undefined });
  expect(useGraphStore.getState().formDrafts).toEqual({});
});

test("encoded profile prefixes keep a slash-named profile independent of another owner's nested scopes", () => {
  seed(
    { first: { kind: "static" }, "first/headers": { kind: "static" } },
    {
      "credentials/first/headers/X-Key/secret_ref|name": draft,
      "credentials/first%2Fheaders|credentials.first/headers.parameters": draft,
    },
  );
  useGraphStore
    .getState()
    .setConfigExtra({ credentials: { first: { kind: "static" } } });
  expect(useGraphStore.getState().formDrafts).toEqual({
    "credentials/first/headers/X-Key/secret_ref|name": draft,
  });
});

test("removing replay configuration clears both documents' drafts without clearing unrelated owners", () => {
  useGraphStore.getState().addReplayConfiguration();
  useGraphStore.setState({
    formDrafts: {
      "core/replay|tls.ca_cert": draft,
      "core/replay/tls|client_cert": draft,
      "replay/database|tls.ca_cert": draft,
      "replay/keys|sample.scopes": draft,
      "replay-other|field": draft,
      "credentials/first|credentials.first.parameters": draft,
    },
  });
  useGraphStore.getState().removeReplayConfiguration();
  expect(useGraphStore.getState().coreReplayConfig).toBeNull();
  expect(useGraphStore.getState().replayServiceConfig).toBeNull();
  expect(useGraphStore.getState().formDrafts).toEqual({
    "replay-other|field": draft,
    "credentials/first|credentials.first.parameters": draft,
  });
});

test("clearing core preserves only the retained replay service's unresolved drafts", () => {
  useGraphStore.getState().addReplayConfiguration();
  const replayServiceConfig = useGraphStore.getState().replayServiceConfig;
  useGraphStore.setState({
    formDrafts: {
      "core/replay|tls.ca_cert": draft,
      "replay/database|tls.ca_cert": draft,
      "replay/keys|sample.scopes": draft,
      "node/sample/code|script": draft,
      "credentials/first|credentials.first.parameters": draft,
    },
  });
  useGraphStore.getState().clearCore();
  expect(useGraphStore.getState().coreReplayConfig).toBeNull();
  expect(useGraphStore.getState().replayServiceConfig).toBe(
    replayServiceConfig,
  );
  expect(useGraphStore.getState().formDrafts).toEqual({
    "replay/database|tls.ca_cert": draft,
    "replay/keys|sample.scopes": draft,
  });
});

test("clearing core clears orphan replay drafts when there is no service document", () => {
  useGraphStore.setState({
    replayServiceConfig: null,
    formDrafts: { "replay/database|tls.ca_cert": draft },
  });
  useGraphStore.getState().clearCore();
  expect(useGraphStore.getState().formDrafts).toEqual({});
});
