import { Accordion, Alert, Select, Stack, Text } from "@mantine/core";
import { useClearDraftBranch } from "./formDrafts";
import { SchemaForm } from "./SchemaForm";
import { NODE_SCHEMAS, schemaProperties, type JSONSchema } from "./nodeSchema";
import { object } from "./FormPresentation";
import { SecretReference } from "./CredentialsPanel";
import {
  WorkspaceEntries,
  credentialNameError,
  record,
} from "./WorkspaceEntries";
export function MCPAuthentication({
  value: auth,
  onChange,
  scope,
  requireAuth = false,
}: {
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
  scope?: string;
  requireAuth?: boolean;
}) {
  const clearDrafts = useClearDraftBranch();
  const schema = schemaProperties(NODE_SCHEMAS.mcp_trigger.schema);
  const keySchema = record(schemaProperties(schema.auth).api_keys)
    .additionalProperties as JSONSchema;
  const authMode = auth.no_auth
    ? "none"
    : auth.oauth
      ? auth.api_keys
        ? "both"
        : "oauth"
      : "key";
  return (
    <Stack gap="sm">
      <Select
        data-field-path="auth"
        label="Client authentication"
        value={authMode}
        allowDeselect={false}
        data={[
          {
            value: "key",
            label: requireAuth
              ? "API keys"
              : "API keys (recommended for a simple setup)",
          },
          { value: "oauth", label: "OAuth identity provider" },
          { value: "both", label: "API keys and OAuth" },
          ...(!requireAuth
            ? [{ value: "none", label: "No authentication — public tools" }]
            : []),
        ]}
        onChange={(mode) => {
          if (!mode) return;
          clearDrafts(scope, "auth");
          onChange(
            mode === "none"
              ? { no_auth: true }
              : {
                  ...(mode === "key" || mode === "both"
                    ? {
                        api_keys:
                          auth.api_keys ??
                          (requireAuth
                            ? {}
                            : {
                                reader: {
                                  secret_ref: {
                                    provider: "env",
                                    name: "MCP_READER_TOKEN",
                                  },
                                },
                              }),
                      }
                    : {}),
                  ...(mode === "oauth" || mode === "both"
                    ? {
                        oauth: auth.oauth ?? { issuer: "", audience: "" },
                      }
                    : {}),
                },
          );
        }}
      />
      {auth.no_auth === true ? (
        <Alert color="orange" title="Public endpoint">
          Anyone who can reach this URL can invoke tools granted anonymous
          access. Configure each tool’s policy explicitly.
        </Alert>
      ) : (
        <>
          <Text size="sm" c="dimmed">
            {requireAuth
              ? auth.api_keys
                ? "Name each caller, then choose where Hookway will read its API key. Enter a secret reference, never the key itself."
                : "Configure the identity provider agents use to connect. Use a dedicated Hookway audience."
              : "These settings contain secret references only. API keys identify their named caller; shared keys grant shared access. OAuth uses your provider’s dedicated Hookway audience."}
          </Text>
          {requireAuth && auth.api_keys && (
            <WorkspaceEntries
              label="caller"
              emptyMessage="Add a caller to connect an agent. You’ll assign its tools next."
              allowRename={false}
              value={auth.api_keys}
              initial={() => ({ secret_ref: { provider: "env", name: "" } })}
              nameError={credentialNameError}
              onChange={(api_keys) => onChange({ ...auth, api_keys })}
            >
              {(name, entry, change) => (
                <Stack gap="sm">
                  <SecretReference
                    compact
                    label="API key secret"
                    value={entry.secret_ref}
                    scope={`${scope}/callers/${name}`}
                    onChange={(secret_ref) => change({ ...entry, secret_ref })}
                  />
                  <Accordion transitionDuration={0}>
                    <Accordion.Item value="expiry">
                      <Accordion.Control>
                        Key expiry (optional)
                      </Accordion.Control>
                      <Accordion.Panel>
                        <SchemaForm
                          schema={keySchema}
                          hideFields={["secret_ref"]}
                          labels={{ expires_at: "Key expiry (optional)" }}
                          value={entry}
                          onChange={(next) => change(object(next))}
                          scope={`${scope}/callers/${name}/expiry`}
                        />
                      </Accordion.Panel>
                    </Accordion.Item>
                  </Accordion>
                </Stack>
              )}
            </WorkspaceEntries>
          )}
          {requireAuth ? (
            auth.oauth && (
              <SchemaForm
                schema={schemaProperties(schema.auth).oauth}
                value={auth.oauth}
                onChange={(oauth) =>
                  onChange({ ...auth, oauth: object(oauth) })
                }
                scope={scope}
                path="auth.oauth"
                groupObjects
              />
            )
          ) : (
            <SchemaForm
              schema={schema.auth}
              value={auth}
              onChange={(next) => onChange(object(next))}
              hideFields={["no_auth"]}
              scope={scope}
              path="auth"
              groupObjects={!requireAuth}
            />
          )}
        </>
      )}
    </Stack>
  );
}
