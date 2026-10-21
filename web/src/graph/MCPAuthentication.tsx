import { Alert, Select, Stack, Text } from "@mantine/core";
import { useClearDraftBranch } from "./formDrafts";
import { SchemaForm } from "./SchemaForm";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";
import { object } from "./FormPresentation";
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
  const authMode = auth.no_auth
    ? "none"
    : auth.oauth
      ? auth.api_keys
        ? "both"
        : "oauth"
      : "key";
  return (
    <Stack gap="sm">
      {" "}
      <Select
        label="Client authentication"
        value={authMode}
        allowDeselect={false}
        data={[
          {
            value: "key",
            label: "API keys (recommended for a simple setup)",
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
                        api_keys: auth.api_keys ?? {
                          reader: {
                            secret_ref: {
                              provider: "env",
                              name: "MCP_READER_TOKEN",
                            },
                          },
                        },
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
          <Text size="xs" c="dimmed">
            These settings contain secret references only. API keys identify
            their named caller; shared keys grant shared access. OAuth uses your
            provider’s dedicated Hookway audience.
          </Text>
          <SchemaForm
            schema={schema.auth}
            value={auth}
            onChange={(next) => onChange(object(next))}
            hideFields={["no_auth"]}
            scope={scope}
            path="auth"
            groupObjects={!requireAuth}
          />
        </>
      )}
    </Stack>
  );
}
