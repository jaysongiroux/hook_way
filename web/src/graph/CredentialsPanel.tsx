import { Alert, Button, Modal, Select, Stack, Text } from "@mantine/core";
import { DARK } from "../tokens";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";
import {
  isSchemaObject,
  schemaProperties,
  type JSONSchema,
} from "./nodeSchema";
import workspaceCatalog from "./workspaceSchema.json";
import {
  credentialNameError,
  record,
  WorkspaceEntries,
} from "./WorkspaceEntries";

const kinds = [
  { value: "static", label: "Static headers from secret references" },
  { value: "oauth_client_credentials", label: "OAuth 2.0 client credentials" },
  { value: "token_exchange", label: "Per-request token exchange" },
];
const credentialMap = workspaceCatalog.credentials as JSONSchema;
const credentialSchema = isSchemaObject(credentialMap)
  ? (credentialMap.additionalProperties as JSONSchema)
  : false;
const properties = schemaProperties(credentialSchema);
const common = [
  "allowed_origins",
  "allow_private_targets",
  "headers",
  "timeout",
];
const kindFields: Record<string, string[]> = {
  static: [],
  oauth_client_credentials: [
    "token_url",
    "client_id",
    "client_secret",
    "client_auth",
    "scope",
    "token_path",
    "instance_path",
    "expiry_path",
    "ttl",
    "invalid_session",
  ],
  token_exchange: [
    "token_url",
    "parameters",
    "secret_parameters",
    "request_format",
    "token_path",
  ],
};
function supportedKind(kind: string): boolean {
  return Object.prototype.hasOwnProperty.call(kindFields, kind);
}
function fieldsForKind(kind: string): string[] {
  return supportedKind(kind) ? kindFields[kind] : [];
}
const required: Record<string, string[]> = {
  static: ["allowed_origins", "headers"],
  oauth_client_credentials: [
    "allowed_origins",
    "token_url",
    "client_id",
    "client_secret",
  ],
  token_exchange: [
    "allowed_origins",
    "token_url",
    "token_path",
    "secret_parameters",
  ],
};
const help: Record<string, string> = {
  allowed_origins:
    "1–64 exact HTTPS origins, such as https://api.example.com. No credentials, query, fragment or path. This allowlist limits where managed credentials can be sent.",
  allow_private_targets:
    "Permit private targets and HTTP origins explicitly. Enable only for trusted deployment endpoints.",
  token_url:
    "HTTPS URL that issues tokens. HTTP requires private-target permission. No embedded credentials, query or fragment.",
  client_auth:
    "OAuth client authentication: HTTP Basic by default, or credentials in the form body.",
  scope: "Optional space-separated OAuth scopes.",
  timeout: "Positive Go duration, at most 30s. Omitted uses 10s.",
  request_format: "Token exchange request format: form by default, or JSON.",
  token_path:
    "Response path to the token (object keys or decimal array indexes). OAuth defaults to access_token; token exchange requires an explicit path.",
  instance_path:
    "Optional OAuth response path for the returned API origin; that origin must match allowed_origins.",
  expiry_path:
    "OAuth response path to expiry seconds; omitted uses expires_in.",
  ttl: "OAuth fallback lifetime, 1s–24h; omitted uses 5m.",
  invalid_session:
    "Optional OAuth response marker that triggers one credential refresh. Path and exact string value are both required.",
  parameters:
    "Nonsecret token-exchange constants only (up to 32). Put credentials in secret parameters. A name cannot appear in both maps.",
  prefix:
    "Optional text prepended to the resolved secret, such as Bearer followed by a space. Never include a literal secret here.",
  provider:
    "Resolve a reference using the process environment, a file, or AWS Secrets Manager.",
  name: "Environment variable name, file path, or AWS secret identifier. This is a reference, not the secret value.",
  selection:
    "AWS Secrets Manager selection policy: exact (default) or newest matching.",
  json_field: "Optional field selected from JSON secret content.",
  refresh: "Refresh interval as a Go duration, 0–24h. Omitted or 0 uses 15m.",
  overlap:
    "Rotation overlap as a Go duration, 0–24h. Omitted uses 0 (no overlap).",
};

export function credentialKindSchema(kind: string): JSONSchema {
  if (!isSchemaObject(credentialSchema)) return false;
  const visible = [
    "kind",
    ...common,
    ...(supportedKind(kind) ? fieldsForKind(kind) : Object.keys(properties)),
  ];
  const fields = Object.fromEntries(
    visible
      .filter((key) => properties[key])
      .map((key) => [key, properties[key]]),
  );
  if (isSchemaObject(fields.invalid_session))
    fields.invalid_session = {
      ...fields.invalid_session,
      required: ["path", "value"],
    };
  return {
    ...credentialSchema,
    properties: fields,
    required: supportedKind(kind) ? required[kind] : [],
  };
}

/** Only an explicit kind switch removes recognized incompatible settings; imported extra fields survive ordinary edits. */
export function changeCredentialKind(
  entry: Record<string, unknown>,
  kind: string,
): Record<string, unknown> {
  const allowed = new Set(["kind", ...common, ...fieldsForKind(kind)]);
  const next = Object.fromEntries(
    Object.entries(entry).filter(
      ([key]) =>
        !Object.prototype.hasOwnProperty.call(properties, key) ||
        allowed.has(key),
    ),
  );
  if (
    kind !== "static" &&
    Object.prototype.hasOwnProperty.call(next, "headers")
  ) {
    next.headers = Object.fromEntries(
      Object.entries(record(next.headers)).filter(
        ([name]) => name.toLowerCase() !== "authorization",
      ),
    );
  }
  return { ...next, kind };
}

export function CredentialsPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const configExtra = useGraphStore((s) => s.configExtra);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Outbound credentials"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Outbound credentials" }}
    >
      <Stack gap="md">
        <Text size="sm" c={DARK.muted}>
          HTTP request nodes reference these profiles by name. Secrets stay in
          environment variables, files or AWS Secrets Manager; this document
          stores references only. Configure allowed origins before attaching a
          profile. This editor does not contact token services or verify live
          credentials.
        </Text>
        <WorkspaceEntries
          label="credential profile"
          value={configExtra.credentials}
          initial={() => ({ kind: "static", allowed_origins: [] })}
          nameError={credentialNameError}
          onChange={(entries) => setConfigExtra({ credentials: entries })}
        >
          {(name, entry, change) => (
            <CredentialEditor name={name} entry={entry} change={change} />
          )}
        </WorkspaceEntries>
      </Stack>
    </Modal>
  );
}

function CredentialEditor({
  name,
  entry,
  change,
}: {
  name: string;
  entry: Record<string, unknown>;
  change: (next: Record<string, unknown>) => void;
}) {
  const kind = String(entry.kind ?? "");
  const supported = supportedKind(kind);
  const scope = `credentials/${encodeURIComponent(name)}`;
  const active = new Set(["kind", ...common, ...fieldsForKind(kind)]);
  const unknown = Object.keys(entry).filter(
    (key) => !Object.prototype.hasOwnProperty.call(properties, key),
  );
  const inactive = supported
    ? Object.keys(entry).filter(
        (key) =>
          Object.prototype.hasOwnProperty.call(properties, key) &&
          !active.has(key),
      )
    : [];
  return (
    <Stack gap="sm">
      <Select
        label="Credential strategy"
        value={kind || null}
        allowDeselect={false}
        description="Changing strategy removes incompatible acquisition settings and any Authorization header owned by a token provider."
        data={
          supported
            ? kinds
            : [
                ...kinds,
                {
                  value: kind || "__missing",
                  label: kind ? `Unsupported: ${kind}` : "Select a strategy",
                },
              ]
        }
        onChange={(next) => {
          if (next && next !== "__missing" && next !== kind)
            change(changeCredentialKind(entry, next));
        }}
      />
      {!supported && (
        <Alert color="orange">
          This imported strategy is unsupported or missing. Its settings are
          retained until you select a supported strategy.
        </Alert>
      )}
      {unknown.length > 0 && (
        <Alert color="orange">
          Unrecognized imported settings are retained in the document:{" "}
          {unknown.join(", ")}. Review the exported YAML before deploying.
        </Alert>
      )}
      {inactive.length > 0 && (
        <Alert color="orange">
          Imported settings inactive for this strategy are retained:{" "}
          {inactive.join(", ")}. Change strategy explicitly or remove these
          settings to resolve the conflict.
          <Button
            variant="light"
            color="orange"
            size="xs"
            mt="xs"
            onClick={() =>
              change(
                Object.fromEntries(
                  Object.entries(entry).filter(
                    ([key]) => !inactive.includes(key),
                  ),
                ),
              )
            }
          >
            Remove inactive settings
          </Button>
        </Alert>
      )}
      <SchemaForm
        schema={credentialKindSchema(kind)}
        value={entry}
        scope={scope}
        path={`credentials.${name}`}
        onChange={(next) => change(record(next))}
        hideFields={[
          "kind",
          "headers",
          "secret_parameters",
          "client_id",
          "client_secret",
        ]}
        order={[
          "allowed_origins",
          "token_url",
          "token_path",
          "client_auth",
          "scope",
          "request_format",
          "parameters",
        ]}
        defaults={{
          timeout: "10s",
          client_auth: "basic",
          request_format: "form",
          token_path:
            kind === "oauth_client_credentials" ? "access_token" : undefined,
          expiry_path: "expires_in",
          ttl: "5m",
        }}
        enums={{
          client_auth: ["basic", "form"],
          request_format: ["form", "json"],
        }}
        fieldHelp={(key) => help[key]}
        labels={{
          allowed_origins: "Allowed origins",
          allow_private_targets: "Allow private targets",
          token_url: "Token endpoint URL",
          parameters: "Nonsecret request parameters",
        }}
      />
      {(kind === "oauth_client_credentials" || !supported) &&
        ["client_id", "client_secret"].map((field) => (
          <SecretReference
            key={field}
            label={
              field === "client_id"
                ? "Client ID reference"
                : "Client secret reference"
            }
            value={entry[field]}
            scope={`${scope}/${field}`}
            onChange={(next) => change({ ...entry, [field]: next })}
          />
        ))}
      <Text size="sm" fw={600}>
        Managed headers{" "}
        {kind === "static" ? "(at least one required)" : "(optional)"}
      </Text>
      {kind === "static" && Object.keys(record(entry.headers)).length === 0 && (
        <Alert color="orange">
          Add at least one managed header with a secret reference.
        </Alert>
      )}
      <WorkspaceEntries
        label="managed header"
        value={entry.headers}
        initial={() => ({ secret_ref: { provider: "env", name: "" } })}
        warning={
          kind !== "static"
            ? "Token providers own Authorization. Additional headers must use other names."
            : "Header values are resolved only at the native HTTP boundary."
        }
        nameError={(header) =>
          [
            "host",
            "content-length",
            "connection",
            "keep-alive",
            "proxy-authenticate",
            "proxy-authorization",
            "te",
            "trailer",
            "transfer-encoding",
            "upgrade",
            "x-hookway-hops",
          ].includes(header.toLowerCase())
            ? "This transport header cannot be managed by a credential profile."
            : !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(header)
              ? "Enter an HTTP header name."
              : Object.keys(record(entry.headers)).some(
                    (existing) =>
                      existing !== header &&
                      existing.toLowerCase() === header.toLowerCase(),
                  )
                ? "That header already exists (header names ignore case)."
                : kind !== "static" && header.toLowerCase() === "authorization"
                  ? "The token provider owns Authorization."
                  : undefined
        }
        onChange={(next) => change({ ...entry, headers: next })}
      >
        {(header, value, update) => (
          <Stack gap="sm">
            <SchemaForm
              schema={{
                type: "object",
                properties: {
                  prefix: schemaProperties(
                    isSchemaObject(properties.headers)
                      ? properties.headers.additionalProperties
                      : false,
                  ).prefix,
                },
              }}
              value={value}
              onChange={(next) => update(record(next))}
              scope={`${scope}/headers/${encodeURIComponent(header)}`}
              fieldHelp={(key) => help[key]}
            />
            <SecretReference
              label="Header secret reference"
              value={value.secret_ref}
              scope={`${scope}/headers/${encodeURIComponent(header)}/secret_ref`}
              onChange={(next) => update({ ...value, secret_ref: next })}
            />
          </Stack>
        )}
      </WorkspaceEntries>
      {(kind === "token_exchange" || !supported) && (
        <>
          <Text size="sm" fw={600}>
            Secret request parameters (at least one required)
          </Text>
          {Object.keys(record(entry.secret_parameters)).length === 0 && (
            <Alert color="orange">
              Add at least one secret request parameter and specify the token
              response path.
            </Alert>
          )}
          <WorkspaceEntries
            label="secret parameter"
            value={entry.secret_parameters}
            initial={() => ({ provider: "env", name: "" })}
            nameError={(parameter) =>
              !parameter ||
              new TextEncoder().encode(parameter).length > 256 ||
              /[\r\n\0]/.test(parameter)
                ? "A parameter name of up to 256 bytes without control characters is required."
                : Object.prototype.hasOwnProperty.call(
                      record(entry.parameters),
                      parameter,
                    )
                  ? "That name is already used by a nonsecret parameter."
                  : undefined
            }
            onChange={(next) => change({ ...entry, secret_parameters: next })}
          >
            {(parameter, value, update) => (
              <SecretReference
                label="Parameter secret reference"
                value={value}
                scope={`${scope}/secret_parameters/${encodeURIComponent(parameter)}`}
                onChange={update}
              />
            )}
          </WorkspaceEntries>
        </>
      )}
    </Stack>
  );
}

function SecretReference({
  label,
  value,
  scope,
  onChange,
}: {
  label: string;
  value: unknown;
  scope: string;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const ref = record(value);
  const provider = String(ref.provider ?? "");
  const schema = properties.client_id;
  const providers = ["env", "file", "aws_secrets_manager"];
  return (
    <Stack gap="xs">
      <Text size="sm" fw={600}>
        {label}
      </Text>
      <Select
        label={`${label} provider`}
        value={provider || null}
        allowDeselect={false}
        data={
          providers.includes(provider) || !provider
            ? providers
            : [...providers, provider]
        }
        description={help.provider}
        onChange={(next) => {
          if (!next || next === provider) return;
          const updated = { ...ref, provider: next };
          if (next !== "aws_secrets_manager")
            delete (updated as Record<string, unknown>).selection;
          onChange(updated);
        }}
      />
      {provider && !providers.includes(provider) && (
        <Alert color="orange">
          Unsupported imported secret provider: {provider}. Its settings are
          retained until you choose a supported provider.
        </Alert>
      )}
      {ref.selection !== undefined && provider !== "aws_secrets_manager" && (
        <Alert color="orange">
          Selection applies only to AWS Secrets Manager. This imported setting
          is retained until you switch provider or remove it.
          <Button
            size="xs"
            variant="light"
            onClick={() => {
              const next = { ...ref };
              delete next.selection;
              onChange(next);
            }}
          >
            Remove selection
          </Button>
        </Alert>
      )}
      <SchemaForm
        schema={schema}
        value={ref}
        scope={scope}
        onChange={(next) => onChange(record(next))}
        hideFields={
          provider === "aws_secrets_manager"
            ? ["provider"]
            : ["provider", "selection"]
        }
        order={["name", "json_field", "selection", "refresh", "overlap"]}
        labels={{ name: `${label} name` }}
        fieldHelp={(key) => help[key]}
      />
    </Stack>
  );
}
